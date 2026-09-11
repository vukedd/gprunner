package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/c12s/gprunner/internal/resolver"
	"github.com/c12s/gprunner/pkg/model"
)

const (
	// rootfsMount tells vfscore to extract the initrd qemu loads onto /. Handing
	// kraft a kernel file rather than a project or a package gets the initrd as far
	// as qemu's -initrd and no further: kraft emits this argument itself only for
	// the latter two, and without it the guest boots with an empty root and cannot
	// find the binary to execute. The doubled quotes escape the inner ones for the
	// CSV parser behind --kernel-arg.
	rootfsMount = `"vfs.fstab=[ ""initrd0:/:extract:::"" ]"`

	// dataSourceMountRoot is where linked data sources appear inside the guest,
	// one mount per data source named after it.
	dataSourceMountRoot = "/data"

	// topicsEnv carries a layer's topics into the guest, comma-separated.
	topicsEnv = "TOPICS"
)

func RunLayer(
	contentKey, imageDir, MQAddr, layerName string,
	ctrl model.Control, ftr model.Features,
	links model.Links,
	datasources map[string]model.DataSource,
	topics []string,
) error {
	binDir := filepath.Join(imageDir, contentKey, "unikraft", "bin")

	// Layers built from a Kraftfile without a rootfs have no initrd.
	initrd := filepath.Join(binDir, "initrd")
	if _, err := os.Stat(initrd); err != nil {
		initrd = ""
	}

	args, err := BuildArgs(filepath.Join(binDir, "kernel"), initrd, MQAddr, layerName, ctrl, ftr, links, datasources, topics)
	if err != nil {
		return err
	}

	fmt.Printf("kraft %s\n", strings.Join(args, " "))

	cmd := exec.Command("kraft", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func BuildArgs(
	kernel, initrd, MQAddr, layerName string,
	ctrl model.Control, ftr model.Features,
	links model.Links,
	datasources map[string]model.DataSource,
	topics []string,
) ([]string, error) {
	args := []string{"run", "--no-prompt"}

	if MQAddr != "" {
		args = append(args, "--network", resolver.BrokerNetworkName)
	}
	if ctrl.Memory != "" {
		args = append(args, "--memory", ctrl.Memory)
	}
	if ctrl.RemoveOnStop {
		args = append(args, "--rm")
	}
	if ctrl.RunDetached {
		args = append(args, "--detach")
	}
	if ctrl.DisableVirtualization {
		args = append(args, "--disable-acceleration")
	}

	// for _, p := range ftr.Ports {
	// 	args = append(args, "--port", p)
	// }

	// chart-declared volumes first, then the linked data sources; both go
	// through the same flag so the guest sees no difference between them
	for _, v := range ftr.Volumes {
		args = append(args, "--volume", v)
	}
	mounts, err := dataSourceMounts(links, datasources)
	if err != nil {
		return nil, err
	}
	for _, m := range mounts {
		args = append(args, "--volume", m)
	}

	for _, e := range ftr.EnvVars {
		args = append(args, "--env", e)
	}
	// stored procedures are booted without a broker address
	if MQAddr != "" {
		args = append(args, "--env", "MQ_ADDR="+MQAddr)
	}
	// the finished, chart-prefixed topic names: the one an event publishes to,
	// or the ones a trigger subscribes to. The guest never builds a topic itself
	if len(topics) > 0 {
		args = append(args, "--env", topicsEnv+"="+strings.Join(topics, ","))
	}

	args = append(args, "--name", layerName)

	if initrd != "" {
		args = append(args, "--rootfs", initrd, "--kernel-arg", rootfsMount)
	}

	args = append(args, kernel)

	if ctrl.KernelArgs != "" {
		args = append(args, "--")
		args = append(args, strings.Fields(ctrl.KernelArgs)...)
	}

	return args, nil
}

func dataSourceMounts(links model.Links, datasources map[string]model.DataSource) ([]string, error) {
	var mounts []string

	for _, name := range links.HardLinks {
		ds, ok := datasources[name]
		if !ok {
			return nil, fmt.Errorf("hard link %q: %w", name, ErrDataSourceUnknown)
		}
		mounts = append(mounts, volumeSpec(ds))
	}

	for _, name := range links.SoftLinks {
		ds, ok := datasources[name]
		if !ok {
			return nil, fmt.Errorf("soft link %q: %w", name, ErrDataSourceUnknown)
		}
		if _, err := os.Stat(ds.Path); err != nil {
			continue // optional, and the resolver has already warned about it
		}
		mounts = append(mounts, volumeSpec(ds))
	}

	return mounts, nil
}

// volumeSpec maps a data source to host:guest. kraft volumes are 9pfs shares
// and can only carry directories, so a data source that points at a file
// shares the directory holding it; the guest then finds the file under
// /data/<name>/<basename>.
func volumeSpec(ds model.DataSource) string {
	host := ds.Path
	if info, err := os.Stat(host); err == nil && !info.IsDir() {
		host = filepath.Dir(host)
	}

	return host + ":" + filepath.Join(dataSourceMountRoot, ds.Name)
}
