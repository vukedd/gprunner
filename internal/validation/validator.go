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

	// soft links aren't mandatory for layer start up
	for _, sl := range links.SoftLinks {
		ds := datasources[sl]
		if err := v.validateDataSource(ds); err != nil {
			v.logger.Warn("soft link validation failed", "layer", md.Name, "dataSource", sl, "err", err)
		}
	}

	return nil
}

func (v *BuildValidator) validateDataSource(ds model.DataSource) error {
	switch ds.Type {
	case model.DataSourceFileType:
		if !resolveDirectory(ds.Path) {
			return fmt.Errorf("data source %s on path %s: %w", ds.Name, ds.Path, ErrDataSourceMissing)
		}
	default:
		return fmt.Errorf("data source %s of type %q: %w", ds.Name, ds.Type, ErrDataSourceType)
	}

	return nil
}

func resolveDirectory(dirPath string) bool {
	return unix.Access(dirPath, unix.F_OK) == nil
}

// Package, image metadata representation
type Package struct {
	Index    string `json:"index"`
	Manifest string `json:"manifest"`
	Plat     string `json:"plat"`
	Version  string `json:"version"`
}

// resolveImage, checks the image cache, pulls image, builds it (remote repo pull), returns contentKey via which
// the image artifact is accessed
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
			return "", fmt.Errorf("layer %q: %w", md.Name, ErrNoTarget)
		}
		pkg := resolvePkgByPlat(pkgs, fts.Targets[0])
		if pkg == nil {
			return "", fmt.Errorf("image %q on platform %s: %w", md.Image, fts.Targets[0], ErrPlatUnavailable)
		}

		spec, buildKey, err = generateBuildKeyFromOCIRef(md, *pkg)
		if err != nil {
			return "", fmt.Errorf("canonicalizing specs: %w", err)
		}
	} else {
		// chart has no image and no build params
		if md.Build == nil {
			return "", fmt.Errorf("layer %q: %w", md.Name, ErrNoSource)
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
		return "", fmt.Errorf("looking up build key: %w", err)
	}

	if ok && contentKey != "" {
		v.logger.Debug("image cache hit", "layer", md.Name, "contentKey", contentKey)
		return contentKey, nil
	}

	v.logger.Info("image cache miss, resolving", "layer", md.Name)

	// a staging directory unique to this attempt: a fixed path would let a
	// failed attempt's leftovers merge into the next one and be hashed into
	// its content key, and would collide between concurrent instantiations
	stageDir, err := os.MkdirTemp(v.bldDir, "stage-"+md.ID+"-")
	if err != nil {
		return "", fmt.Errorf("creating staging directory: %w", err)
	}

	// publishing renames the directory away, so this is a no-op on the success
	// path and a rollback on every failure below
	defer v.removeAll(stageDir)

	if err := v.downloadImage(ctx, md, stageDir); err != nil {
		return "", fmt.Errorf("fetching image for layer %q: %w", md.Name, err)
	}

	contentKey, size, err := generateContentKey(stageDir)
	if err != nil {
		return "", fmt.Errorf("hashing image for layer %q: %w", md.Name, err)
	}

	// an existing directory already holds these exact bytes, so the staging
	// copy is redundant and the deferred cleanup discards it
	artifactDir := filepath.Join(v.imgDir, contentKey)
	if !resolveDirectory(artifactDir) {
		if err := os.Rename(stageDir, artifactDir); err != nil {
			return "", fmt.Errorf("publishing image %s: %w", artifactDir, err)
		}
	}

	// the artifact is content-addressed, so a failure here leaves a directory
	// that is valid but unindexed: the next resolution of this layer
	// recomputes the same content key and reuses it
	if err := v.store.SaveImageMetadata(ctx, spec, buildKey, contentKey, size); err != nil {
		return "", fmt.Errorf("indexing image %s: %w", contentKey, err)
	}

	v.logger.Info("image published", "layer", md.Name, "contentKey", contentKey, "bytes", size)

	return contentKey, nil
}

// downloadImage populates stageDir with the layer's build artifacts, either by
// pulling a published image or by cloning and building the layer's repository.
// !!! The caller owns stageDir and is responsible for removing it !!!
func (v *BuildValidator) downloadImage(ctx context.Context, md model.LayerMetadata, stageDir string) error {
	if hasImage(md) {
		cmd := exec.CommandContext(ctx, "kraft", "pkg", "pull", "--no-prompt", "-o", stageDir, md.Image)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("pull failed for %q: %w — %s", md.Image, err, output)
		}

		return nil
	}

	// command token extraction
	cmdFields, err := buildFields(md)
	if err != nil {
		return err
	}

	// clone dir is unique per attempt, otherwise concurrent builds of one layer would share it
	tmpRepoDir, err := os.MkdirTemp(v.bldDir, "repo-"+md.ID+"-")
	if err != nil {
		return fmt.Errorf("creating clone directory: %w", err)
	}
	defer v.removeAll(tmpRepoDir)

	// if prompted abort to avoid infinite wait time
	cloneCmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", md.Build.Pull, tmpRepoDir)
	cloneCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")

	cloneOutput, err := cloneCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("repo clone failed for %q: %w — %s", md.Build.Pull, err, cloneOutput)
	}

	// build image
	workDir := filepath.Join(tmpRepoDir, path.Clean("/"+md.Build.Workdir))

	args := append([]string{}, cmdFields[1:]...)
	args = append(args, workDir)

	buildCmd := exec.CommandContext(ctx, cmdFields[0], args...)
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("image build failed for %q: %w — %s", md.Name, err, buildOutput)
	}

	// pack kernels (and optionally initrd) into the staging directory
	return v.packageLayer(ctx, md.Name, workDir, stageDir, cmdFields)
}

// generateContentKey, generates contentKey which uniquely identifies cached image
// artifacts
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

// resolveCommit, resolves pull repo default branch HEAD digest
func resolveCommit(ctx context.Context, pull string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", pull, "HEAD")
	// if prompted abort to avoid infinite wait time
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")

	out, err := cmd.Output()
	if err != nil {
		// if present, extracts the error message from the output
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("resolving %s: %w — %s", pull, err, ee.Stderr)
		}
		return "", fmt.Errorf("resolving %s: %w", pull, err)
	}

	// ls-remote exits 0 and prints nothing when the ref does not exist
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("%s: %w", pull, ErrNoRemoteRef)
	}
	return fields[0], nil
}

// resolvePkgByPlat, returns package for provided platform
func resolvePkgByPlat(pkgs []Package, plat string) *Package {
	for _, pkg := range pkgs {
		if pkg.Plat == plat {
			return &pkg
		}
	}

	return nil
}

// generateBuildKeyFromOCIRef, generates buildKey which will potentially help us avoid the costs
// of building an image. Returns spec (buildParams, persisted in db for debugging), buildKey, error
func generateBuildKeyFromOCIRef(md model.LayerMetadata, pkg Package) (string, string, error) {
	specMap := make(map[string]string)
	specMap["kind"], specMap["digest"], specMap["plat"], specMap["ref"] = "OCI", pkg.Manifest, pkg.Plat, md.Image

	return hashSpec(specMap)
}

// generateBuildKeyFromRemoteRepo, generates buildKey which will potentially help us avoid the costs
// of building an image. Returns spec (buildParams, persisted in db for debugging), buildKey, error
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

// hashSpec canonicalizes a build spec and derives its build key.
func hashSpec(specMap map[string]string) (spec string, key string, err error) {
	specBytes, err := json.Marshal(specMap)
	if err != nil {
		return "", "", fmt.Errorf("encoding build spec: %w", err)
	}

	sum := sha256.Sum256(specBytes)
	return string(specBytes), hex.EncodeToString(sum[:]), nil
}

// buildFields, tokenizes build commands
func buildFields(md model.LayerMetadata) ([]string, error) {
	fields := strings.Fields(md.Build.Command)
	if len(fields) == 0 {
		return nil, fmt.Errorf("layer %q: %w", md.Name, ErrEmptyCommand)
	}

	return fields, nil
}

func (v *BuildValidator) removeAll(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		v.logger.Warn("removing directory", "dir", dir, "err", err)
	}
}

func hasImage(md model.LayerMetadata) bool {
	return strings.Trim(md.Image, " ") != ""
}
