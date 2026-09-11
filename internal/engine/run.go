package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/c12s/gprunner/internal/resolver"
)

// rootfsMount tells vfscore to extract the initrd qemu loads onto /. Handing
// kraft a kernel file rather than a project or a package gets the initrd as far
// as qemu's -initrd and no further: kraft emits this argument itself only for
// the latter two, and without it the guest boots with an empty root and cannot
// find the binary to execute. The doubled quotes escape the inner ones for the
// CSV parser behind --kernel-arg.
const rootfsMount = `"vfs.fstab=[ ""initrd0:/:extract:::"" ]"`

func RunLayer(contentKey, memory, kernelArgs, imageDir, MQAddr string) error {
	binDir := filepath.Join(imageDir, contentKey, "unikraft", "bin")

	args := []string{"run", "--no-prompt", "--plat", "qemu", "--arch", "x86_64", "--network", resolver.BrokerNetworkName, "-e", "MQ_ADDR=" + MQAddr, "--rm"}

	// Layers built from a Kraftfile without a rootfs have no initrd.
	initrd := filepath.Join(binDir, "initrd")
	if _, err := os.Stat(initrd); err == nil {
		args = append(args, "--rootfs", initrd, "--kernel-arg", rootfsMount)
	}

	args = append(args, filepath.Join(binDir, "kernel"))
	if kernelArgs != "" {
		args = append(args, "--")
		args = append(args, strings.Fields(kernelArgs)...)
	}

	fmt.Printf("kraft %s\n", strings.Join(args, " "))

	cmd := exec.Command("kraft", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
