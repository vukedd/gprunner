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
	datasources, imageMap := layers.DataSources, make(map[string]string)

	for _, pcd := range procedures {
		contentKey, err := v.validateLayer(ctx, pcd.Features, pcd.Metadata, pcd.Links, datasources)
		if err != nil {
			return nil, err
		}

		imageMap[pcd.Metadata.ID] = contentKey
	}

	for _, et := range triggers {
		contentKey, err := v.validateLayer(ctx, et.Features, et.Metadata, et.Links, datasources)
		if err != nil {
			return nil, err
		}

		imageMap[et.Metadata.ID] = contentKey
	}

	// event structure doesn't contain links so empty struct is passed
	for _, e := range events {
		contentKey, err := v.validateLayer(ctx, e.Features, e.Metadata, model.Links{}, datasources)
		if err != nil {
			return nil, err
		}

		imageMap[e.Metadata.ID] = contentKey
	}

	return imageMap, nil
}

func (v *BuildValidator) validateLayer(ctx context.Context, fts model.Features, md model.LayerMetadata, links model.Links, datasources map[string]model.DataSource) (string, error) {
	if err := v.validateLinks(links, md, datasources); err != nil {
		return "", err
	}

	contentKey, err := v.resolveImage(ctx, md, fts)
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
			return fmt.Errorf("hard link validation failed: %w", err)
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

func resolveDirectory(dirPath string) bool {
	return unix.Access(dirPath, unix.F_OK) == nil
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
		infoCmd := exec.CommandContext(ctx, "kraft", "pkg", "info", "-u", "-o", "json", md.Image)

		output, err := infoCmd.Output()
		if err != nil {
			// extracts the error from the output
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return "", fmt.Errorf("inspecting image %q: %w — %s", md.Image, err, ee.Stderr)
			}
			return "", fmt.Errorf("inspecting image %q: %w", md.Image, err)
		}

		var pkgs []Package
		if err := json.Unmarshal(output, &pkgs); err != nil {
			return "", fmt.Errorf("unmarshaling image package: %w", err)
		}

		if len(fts.Targets) == 0 {
			return "", fmt.Errorf("no target provided for layer %s", md.Name)
		}
		pkg := resolvePkgByPlat(pkgs, fts.Targets[0])
		if pkg == nil {
			return "", fmt.Errorf("no package for this platform: %s", fts.Targets[0])
		}

		spec, buildKey, err = generateBuildKeyFromOCIRef(md, *pkg)
		if err != nil {
			return "", fmt.Errorf("canonicalizing specs: %w", err)
		}
	} else {
		// chart has no image and no build params
		if md.Build == nil {
			return "", fmt.Errorf("layer %q has no image reference and no build config", md.Name)
		}

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

	if contentKey == "" || !ok {
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

	if hasImage(md) {
		cmd := exec.CommandContext(ctx, "kraft", "pkg", "pull", "--no-prompt", "-o", outputDir, md.Image)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pull failed for %q: %w — %s", md.Image, err, output)
		}
	} else {
		// command token extraction
		cmdFields, err := buildFields(md)
		if err != nil {
			return "", err
		}

		// clone repo
		tmpRepoDir := filepath.Join(v.bldDir, md.ID)
		defer os.RemoveAll(tmpRepoDir)

		cloneCmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", md.Build.Pull, tmpRepoDir)
		cloneCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")

		cloneOutput, err := cloneCmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("repo clone failed for %q: %w — %s", md.Build.Pull, err, cloneOutput)
		}

		// build image
		workDir := filepath.Join(tmpRepoDir, path.Clean("/"+md.Build.Workdir))

		args := append([]string{}, cmdFields[1:]...)
		args = append(args, workDir)

		buildCmd := exec.CommandContext(ctx, cmdFields[0], args...)
		buildOutput, err := buildCmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("image build failed for %q: %w — %s", md.Name, err, buildOutput)
		}

		// pack kernels (and optionally initrd) into to the outputDir and returns it
		if err := packageLayer(ctx, md.ID, workDir, outputDir, cmdFields); err != nil {
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
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("resolving %s: %w — %s", pull, err, ee.Stderr)
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

	return hashSpec(specMap)
}

func generateBuildKeyFromRemoteRepo(md model.LayerMetadata, commitSHA string) (spec string, key string, err error) {
	fields, err := buildFields(md)
	if err != nil {
		return "", "", err
	}

	specMap := make(map[string]string)
	specMap["kind"], specMap["sha"] = "GIT", commitSHA
	specMap["workdir"], specMap["pull"] = path.Clean("/"+md.Build.Workdir), md.Build.Pull
	specMap["command"] = strings.Join(fields, "\x00")

	return hashSpec(specMap)
}

func hasImage(md model.LayerMetadata) bool {
	return strings.Trim(md.Image, " ") != ""
}

// hashSpec canonicalizes a build spec and derives its build key. json.Marshal
// canonizes the fields
func hashSpec(specMap map[string]string) (spec string, key string, err error) {
	specBytes, err := json.Marshal(specMap)
	if err != nil {
		return "", "", fmt.Errorf("encoding build spec: %w", err)
	}

	sum := sha256.Sum256(specBytes)
	return string(specBytes), hex.EncodeToString(sum[:]), nil
}

func buildFields(md model.LayerMetadata) ([]string, error) {
	fields := strings.Fields(md.Build.Command)
	if len(fields) == 0 {
		return nil, fmt.Errorf("layer %q has an empty build command", md.Name)
	}

	return fields, nil
}
