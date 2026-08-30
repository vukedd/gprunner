package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/c12s/pgrunner/internal/persistence"
	"github.com/c12s/pgrunner/pkg/model"

	"golang.org/x/sys/unix"
)

var artifacts = []string{"kernel", "kernel.dbg", "initrd"}

type BuildValidator struct {
	imgDir string
	bldDir string
	store  *persistence.Store
	logger *slog.Logger
}

func NewBuildValidator(l *slog.Logger, imgDir, bldDir string, s *persistence.Store) *BuildValidator {
	return &BuildValidator{logger: l, imgDir: imgDir, bldDir: bldDir, store: s}
}

// general
func (v *BuildValidator) ValidateLayers(ctx context.Context, layers model.ChartConfig) (map[string]string, error) {
	procedures, triggers, events := layers.StoredProcedures, layers.EventTriggers, layers.Events
	datasources := layers.DataSources

	imageMap := make(map[string]string)

	for _, pcd := range procedures {
		if contentKey, err := v.validateLayer(ctx, pcd.Features, pcd.Metadata, pcd.Links, datasources); err != nil {
			return nil, err
		} else {
			imageMap[pcd.Metadata.ID] = contentKey
		}
	}

	for _, et := range triggers {
		if contentKey, err := v.validateLayer(ctx, et.Features, et.Metadata, et.Links, datasources); err != nil {
			return nil, err
		} else {
			imageMap[et.Metadata.ID] = contentKey
		}
	}

	// event structure doesn't contain links so empty struct is passed
	for _, e := range events {
		if contentKey, err := v.validateLayer(ctx, e.Features, e.Metadata, model.Links{}, datasources); err != nil {
			return nil, err
		} else {
			imageMap[e.Metadata.ID] = contentKey
		}
	}

	return imageMap, nil
}

func (v *BuildValidator) validateLayer(ctx context.Context, fts model.Features, md model.LayerMetadata, links model.Links, datasources map[string]model.DataSource) (string, error) {
	if err := v.validateLinks(links, md, datasources); err != nil {
		return "", err
	}

	contentKey, err := v.validateLayerImage(ctx, md, fts)
	if err != nil {
		return "", err
	}

	return contentKey, nil
}

// data source validation
func (v *BuildValidator) validateLinks(links model.Links, md model.LayerMetadata, datasources map[string]model.DataSource) error {
	for _, hl := range links.HardLinks {
		ds := datasources[hl]
		if err := v.validateDataSource(ds); err != nil {
			return fmt.Errorf("hard link validation failed: %v\n", err)
		}
	}

	for _, sl := range links.SoftLinks {
		ds := datasources[sl]
		if err := v.validateDataSource(ds); err != nil {
			fmt.Printf("soft link validation failed: %v\n", err)
		}
	}

	return nil
}

func (v *BuildValidator) validateDataSource(ds model.DataSource) error {
	switch ds.Type {
	case model.DataSourceFileType:
		if !resolveDirectory(ds.Path) {
			return fmt.Errorf("datasource %s on path %s not found", ds.Name, ds.Path)
		}
	default:
		return fmt.Errorf("invalid data source type: %v", ds.Type)
	}

	return nil
}

func resolveDirectory(path string) bool {
	return unix.Access(path, unix.F_OK) == nil
}

// image validation
func (v *BuildValidator) validateLayerImage(ctx context.Context, md model.LayerMetadata, fts model.Features) (string, error) {
	contentKey, err := v.resolveImage(ctx, md, fts)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return contentKey, nil
}

type Package struct {
	Index    string `json:"index"`
	Manifest string `json:"manifest"`
	Plat     string `json:"plat"`
	Version  string `json:"version"`
}

func (v *BuildValidator) resolveImage(ctx context.Context, md model.LayerMetadata, fts model.Features) (string, error) {

	var spec, buildKey string
	if hasImage(md) {
		infoCmd := exec.Command("kraft", "pkg", "info", "-u", "-o", "json", md.Image)

		output, err := infoCmd.Output()
		if err != nil {
			return "", fmt.Errorf("pull failed for %q: %v — %s", md.Image, err, output)
		}

		var pkgs []Package
		if err := json.Unmarshal(output, &pkgs); err != nil {
			return "", fmt.Errorf("unmarshaling image package: %w", err)
		}

		pkg := resolvePkgByPlat(pkgs, fts.Targets[0])
		if pkg == nil {
			return "", fmt.Errorf("fetching fetching image (%s) for plat %s: %w", md.Image, fts.Targets[0], err)
		}

		spec, buildKey, err = generateBuildKeyFromOCIRef(md, *pkg)
		if err != nil {
			return "", fmt.Errorf("canonicalizing specs: %w", err)
		}
	} else {
		commitSHA, err := resolveCommit(ctx, md.Build.Pull)
		if err != nil {
			return "", fmt.Errorf("resolving commit: %w", err)
		}

		spec, buildKey, err = generateBuildKeyFromRemoteRepo(md, commitSHA)
		if err != nil {
			return "", fmt.Errorf("canonicalizing specs: %w", err)
		}
	}

	contentKey, ok, err := v.store.GetContentKeyByBuildKey(ctx, buildKey)
	if err != nil {
		return "", fmt.Errorf("fetching contentKey: %w", err)
	}

	if contentKey == "" && !ok {
		imageTempDir, err := v.downloadImage(ctx, md)
		if err != nil {
			return "", fmt.Errorf("downloading image: %w", err)
		}

		newContentKey, size, err := generateContentKey(imageTempDir)
		if err != nil {
			return "", fmt.Errorf("building contentKey: %w", err)
		}

		artifactDir := filepath.Join(v.imgDir, newContentKey)
		if resolveDirectory(artifactDir) {
			if err := os.RemoveAll(imageTempDir); err != nil {
				return "", fmt.Errorf("removing staging dir %s: %w", imageTempDir, err)
			}
		} else if err := os.Rename(imageTempDir, artifactDir); err != nil {
			return "", fmt.Errorf("publishing image %s: %w", artifactDir, err)
		}

		if err = v.store.SaveImageMetadata(ctx, spec, buildKey, newContentKey, size); err != nil {
			return "", fmt.Errorf("saving image metadata: %w", err)
		}

		contentKey = newContentKey
	}

	return contentKey, nil
}

func (v *BuildValidator) downloadImage(ctx context.Context, md model.LayerMetadata) (string, error) {
	// image build result location
	outputDir := filepath.Join(v.imgDir, "stage-"+md.ID)

	if md.Image != "" {
		cmd := exec.Command("kraft", "pkg", "pull", "--no-prompt", "-o", outputDir, md.Image)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pull failed for %q: %v — %s", md.Image, err, output)
		}
	} else {
		// chart has no image and no build params
		if md.Build == nil {
			return "", fmt.Errorf("layer %q has no image reference and no build config", md.Name)
		}

		// command token extraction
		cmdFields := strings.Fields(md.Build.Command)
		if len(cmdFields) == 0 {
			return "", fmt.Errorf("layer %q has an empty build command", md.Name)
		}

		// clone repo
		tmpRepoDir := filepath.Join(v.bldDir, md.ID)
		defer os.RemoveAll(tmpRepoDir)

		cloneCmd := exec.Command("git", "clone", "--depth", "1", md.Build.Pull, tmpRepoDir)
		cloneOutput, err := cloneCmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("repo clone failed for %q: %v — %s", md.Image, err, cloneOutput)
		}

		// build image
		workDir := filepath.Join(tmpRepoDir, md.Build.Workdir)

		args := append([]string{}, cmdFields[1:]...)
		args = append(args, workDir)

		buildCmd := exec.Command(cmdFields[0], args...)
		buildOutput, err := buildCmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("image build failed for %q: %v — %s", md.Name, err, buildOutput)
		}

		// pack kernels (and optionally initrd) into to the outputDir and returns it
		if err := packageLayer(md.ID, workDir, outputDir, cmdFields); err != nil {
			return "", err
		}
	}

	return outputDir, nil
}

func generateContentKey(imageDir string) (id string, size int64, err error) {
	outer := sha256.New()

	for _, name := range artifacts {
		f, err := os.Open(filepath.Join(imageDir, "unikraft", "bin", name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", 0, fmt.Errorf("hashing %s: %w", name, err)
		}

		inner := sha256.New()
		n, err := io.Copy(inner, f)
		f.Close()
		if err != nil {
			return "", 0, fmt.Errorf("hashing %s: %w", name, err)
		}

		size += n
		fmt.Fprintf(outer, "%s\x00%x\n", name, inner.Sum(nil))
	}

	return hex.EncodeToString(outer.Sum(nil)), size, nil
}

func resolveCommit(ctx context.Context, pull string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", pull, "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("resolving %s: %v — %s", pull, err, ee.Stderr)
		}
		return "", fmt.Errorf("resolving %s: %w", pull, err)
	}

	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("no HEAD ref in %s", pull)
	}
	return fields[0], nil
}

func resolvePkgByPlat(pkgs []Package, plat string) *Package {
	for _, pkg := range pkgs {
		if pkg.Plat == plat {
			return &pkg
		}
	}

	return nil
}

func generateBuildKeyFromOCIRef(md model.LayerMetadata, pkg Package) (string, string, error) {
	specMap := make(map[string]string)
	specMap["kind"], specMap["digest"], specMap["plat"], specMap["ref"] = "OCI", pkg.Manifest, pkg.Plat, md.Image

	specBytes, err := json.Marshal(specMap)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(specBytes)
	return string(specBytes), hex.EncodeToString(sum[:]), nil
}

func generateBuildKeyFromRemoteRepo(md model.LayerMetadata, commitSHA string) (spec string, key string, err error) {
	specMap := make(map[string]string)
	specMap["kind"], specMap["sha"] = "GIT", commitSHA
	specMap["workdir"], specMap["pull"] = path.Clean("/"+md.Build.Workdir), md.Build.Pull
	specMap["command"] = strings.Join(strings.Fields(md.Build.Command), "\x00")

	specBytes, err := json.Marshal(specMap)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(specBytes)
	return string(specBytes), hex.EncodeToString(sum[:]), nil
}

func hasImage(md model.LayerMetadata) bool {
	return strings.Trim(md.Image, " ") != ""
}
