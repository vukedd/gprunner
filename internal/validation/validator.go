package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/c12s/runner/internal/model"
	"golang.org/x/sys/unix"
)

var (
	imageDir   = os.Getenv("IMAGE_DIR")
	buildDir   = os.Getenv("BUILD_DIR")
	versionTag = os.Getenv("VERSION_TAG")
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
		if err := v.validateLayer(pcd.Features, pcd.Metadata, pcd.Links, datasources); err != nil {
			return err
		}
	}

	for _, et := range triggers {
		if err := v.validateLayer(et.Features, et.Metadata, et.Links, datasources); err != nil {
			return err
		}
	}

	// event structure doesn't contain links so empty struct is passed
	for _, e := range events {
		if err := v.validateLayer(e.Features, e.Metadata, model.Links{}, datasources); err != nil {
			return err
		}
	}

	return nil
}

func (v *BuildValidator) validateLayer(fts model.Features, md model.LayerMetadata, links model.Links, datasources map[string]model.DataSource) error {
	if err := v.validateLinks(links, md, datasources); err != nil {
		return err
	}

	return v.validateLayerImage(md, fts)
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
func (v *BuildValidator) validateLayerImage(md model.LayerMetadata, fts model.Features) error {
	// if isLayerImageSaved(md.ID) {
	// 	fmt.Printf("image is already downloaded. Layer Name: %s\n", md.Name)
	// 	return nil
	// }

	// if err := downloadImage(md); err != nil {
	// 	return err
	// }

	if err := resolveImage(md, fts); err != nil {
		return fmt.Errorf("%w", err)
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

func resolveImage(md model.LayerMetadata, fts model.Features) error {

	if hasImage(md) {
		infoCmd := exec.Command("kraft", "pkg", "info", "-u", "-o", "json", md.Image)

		output, err := infoCmd.Output()
		if err != nil {
			return fmt.Errorf("pull failed for %q: %v — %s", md.Image, err, output)
		}

		var pkgs []model.Package
		if err := json.Unmarshal(output, &pkgs); err != nil {
			return fmt.Errorf("An error has ocurred while unmarshaling image package: %w", err)
		}

		pkg := resolvePkgByPlat(pkgs, fts.Targets[0])
		if pkg == nil {
			return fmt.Errorf("An error occurred while fetching fetching image (%s) for plat %s: %w", md.Image, fts.Targets[0], err)
		}

		_, err = generateBuildKey(md, *pkg)
		if err != nil {
			return fmt.Errorf("an error has occurred while canonicalizing specs: %w", err)
		}

	}

	return nil
}

func resolvePkgByPlat(pkgs []model.Package, plat string) *model.Package {
	for _, pkg := range pkgs {
		if pkg.Plat == plat {
			return &pkg
		}
	}

	return nil
}

func generateBuildKey(md model.LayerMetadata, pkg model.Package) (string, error) {
	if hasImage(md) {
		specMap := make(map[string]string)
		specMap["kind"], specMap["digest"], specMap["plat"], specMap["ref"] = "OCI", pkg.Manifest, pkg.Plat, md.Image

		out, err := json.Marshal(specMap)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(out)
		return versionTag + hex.EncodeToString(sum[:]), nil
	}

	return "", nil
}

func hasImage(md model.LayerMetadata) bool {
	return strings.Trim(md.Image, " ") != ""
}
