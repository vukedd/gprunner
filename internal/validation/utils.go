package validation

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// blobPath, converts digest into blob path
func blobPath(tmp, digest string) string {
	algorithm, hex, _ := strings.Cut(digest, ":")
	return filepath.Join(tmp, "blobs", algorithm, hex)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

// untar, takes archive and the destination where the archive should be unpacked
func untar(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := tar.NewReader(f)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dst, filepath.Clean("/"+header.Name))
		if target != dst && !strings.HasPrefix(target, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("entry %q escapes the destination directory", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}

			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, reader); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

// extractPackage,
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

// packageLayer,
func (v *BuildValidator) packageLayer(ctx context.Context, layerName, workDir, outputDir string, buildFields []string) error {
	pkgName := filepath.Base(outputDir)

	// rebuild rootfs and fetch kernels from store (downloads on cache miss)
	args := []string{"pkg", "--no-prompt", "--name", pkgName}

	if plat, arch := targetOf(buildFields); plat != "" && arch != "" {
		args = append(args, "--plat", plat, "--arch", arch)
	}
	args = append(args, workDir)

	output, err := exec.CommandContext(ctx, "kraft", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("packaging failed for layer %q: %w — %s", layerName, err, output)
	}

	defer func() {
		out, err := exec.CommandContext(ctx, "kraft", "pkg", "remove", "--no-prompt", "-n", pkgName).CombinedOutput()
		if err != nil {
			v.logger.Warn("removing package from kraft store", "pkg", pkgName, "err", err, "output", out)
		}
	}()

	// export stored package as tar
	archive := filepath.Join(workDir, "package.tar")

	output, err = exec.CommandContext(ctx, "kraft", "pkg", "export", "--output", archive, pkgName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("package export failed for layer %q: %w — %s", layerName, err, output)
	}

	// extract package to outputDir
	if err := extractPackage(archive, outputDir); err != nil {
		return fmt.Errorf("unpacking package for layer %q: %w", layerName, err)
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
