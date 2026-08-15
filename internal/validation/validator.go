package validation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/c12s/runner/internal/model"
	"golang.org/x/sys/unix"
)

var (
	imageDir = os.Getenv("IMAGE_DIR")
	buildDir = os.Getenv("BUILD_DIR")
)

type BuildValidator struct{}

func NewBuildValidator() *BuildValidator {
	return &BuildValidator{}
}

// general
func (v *BuildValidator) ValidateLayers(layers model.ChartConfig) error {
	procedures, triggers, events := layers.StoredProcedures, layers.EventTriggers, layers.Events
	datasources := layers.DataSources

	for _, pcd := range procedures {
		if err := v.validateLayer(pcd.Metadata, pcd.Links, datasources); err != nil {
			return err
		}
	}

	for _, et := range triggers {
		if err := v.validateLayer(et.Metadata, et.Links, datasources); err != nil {
			return err
		}
	}

	// event structure doesn't contain links so empty struct is passed
	for _, e := range events {
		if err := v.validateLayer(e.Metadata, model.Links{}, datasources); err != nil {
			return err
		}
	}

	return nil
}

func (v *BuildValidator) validateLayer(md model.LayerMetadata, links model.Links, datasources map[string]model.DataSource) error {
	if err := v.validateLinks(links, md, datasources); err != nil {
		return err
	}

	return v.validateLayerImage(md)
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
		if err := checkFileAccess(ds); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid data source type: %v", ds.Type)
	}

	return nil
}

func checkFileAccess(ds model.DataSource) error {
	if exists := unix.Access(ds.Path, unix.F_OK) == nil; !exists {
		return fmt.Errorf("an error has occurred while validating %s on path %s", ds.Name, ds.Path)
	}

	return nil
}

// image validation
func (v *BuildValidator) validateLayerImage(md model.LayerMetadata) error {
	if isLayerImageSaved(md.ID) {
		fmt.Printf("image is already downloaded. Layer Name: %s\n", md.Name)
		return nil
	}

	if err := downloadImage(md); err != nil {
		return err
	}

	return nil
}

func isLayerImageSaved(layerID string) bool {
	return unix.Access(filepath.Join(imageDir, layerID, "unikraft/bin/kernel"), unix.F_OK) == nil
}

func downloadImage(md model.LayerMetadata) error {
	// image build result location
	outputDir := filepath.Join(imageDir, md.ID)

	if md.Image != "" {
		cmd := exec.Command("kraft", "pkg", "pull", "--no-prompt", "-o", outputDir, md.Image)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("pull failed for %q: %v — %s", md.Image, err, output)
		}
	} else {
		// chart has no image and no build params
		if md.Build == nil {
			return fmt.Errorf("layer %q has no image reference and no build config", md.Name)
		}

		// command token extraction
		cmdFields := strings.Fields(md.Build.Command)
		if len(cmdFields) == 0 {
			return fmt.Errorf("layer %q has an empty build command", md.Name)
		}

		// clone repo
		tmpRepoDir := filepath.Join(buildDir, md.ID)
		defer os.RemoveAll(tmpRepoDir)

		cloneCmd := exec.Command("git", "clone", "--depth", "1", md.Build.Pull, tmpRepoDir)
		cloneOutput, err := cloneCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("repo clone failed for %q: %v — %s", md.Image, err, cloneOutput)
		}

		// build image
		workDir := filepath.Join(tmpRepoDir, md.Build.Workdir)

		args := append([]string{}, cmdFields[1:]...)
		args = append(args, workDir)

		buildCmd := exec.Command(cmdFields[0], args...)
		buildOutput, err := buildCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("image build failed for %q: %v — %s", md.Name, err, buildOutput)
		}

		// pack kernels (and optionally initrd) into to the outputDir
		if err := packageLayer(md.ID, workDir, outputDir, cmdFields); err != nil {
			return err
		}
	}

	return nil
}

func packageLayer(layerID, workDir, outputDir string, buildFields []string) error {

	// rebuild rootfs and fetch kernels from store (downloads on cache miss)
	args := []string{"pkg", "--no-prompt", "--name", layerID}

	if plat, arch := targetOf(buildFields); plat != "" && arch != "" {
		args = append(args, "--plat", plat, "--arch", arch)
	}
	args = append(args, workDir)

	output, err := exec.Command("kraft", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("packaging failed for layer %q: %v — %s", layerID, err, output)
	}

	// tar manifests package
	archive := filepath.Join(workDir, "package.tar")

	output, err = exec.Command("kraft", "pkg", "export", "--output", archive, layerID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("package export failed for layer %q: %v — %s", layerID, err, output)
	}

	// extract package to outputDir
	if err := extractPackage(archive, outputDir); err != nil {
		return fmt.Errorf("unpacking package for layer %q: %w", layerID, err)
	}

	return nil
}

func extractPackage(packageArchivePath, outputDir string) error {
	// outer container untar
	// e.g. package structure:
	// 	- index.json, oci-layout
	//  - blobs/sha256/manifest.json
	//  - multiple blobs/sha256/tars (kernels and initrd)
	tmp, err := os.MkdirTemp("", "kraft-pkg-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	if err := untar(packageArchivePath, tmp); err != nil {
		return fmt.Errorf("unpacking exported package archive: %w", err)
	}

	// unmarshal index structure with neccessary manifest data
	var index struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := readJSON(filepath.Join(tmp, "index.json"), &index); err != nil {
		return fmt.Errorf("reading package index: %w", err)
	}

	// each manifest lists layer blobs; a layer is a tar containing a single
	// unikraft/bin/* file, so unpacking them all into outputDir merges into
	// one tree (kernel, kernel.dbg, initrd)
	for _, m := range index.Manifests {
		var manifest struct {
			Layers []struct {
				Digest string `json:"digest"`
			} `json:"layers"`
		}
		if err := readJSON(blobPath(tmp, m.Digest), &manifest); err != nil {
			return fmt.Errorf("reading manifest %s: %w", m.Digest, err)
		}

		for _, l := range manifest.Layers {
			if err := untar(blobPath(tmp, l.Digest), outputDir); err != nil {
				return fmt.Errorf("unpacking layer %s: %w", l.Digest, err)
			}
		}
	}

	return nil
}

func targetOf(buildFields []string) (plat string, arch string) {
	for i, f := range buildFields {
		if i+1 >= len(buildFields) {
			break
		}

		switch f {
		case "--plat", "-p":
			plat = buildFields[i+1]
		case "--arch", "-m":
			arch = buildFields[i+1]
		}
	}

	return plat, arch
}
