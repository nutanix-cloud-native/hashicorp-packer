// The linode package contains a packer.Builder implementation
// that builds Linode images.
package linode

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer/common"
	"github.com/linode/linodego"

	"github.com/hashicorp/packer/helper/communicator"
	"github.com/hashicorp/packer/helper/multistep"
	"github.com/hashicorp/packer/packer"
)

// The unique ID for this builder.
const BuilderID = "packer.linode"

// Builder represents a Packer Builder.
type Builder struct {
	config Config
	runner multistep.Runner
}

func (b *Builder) ConfigSpec() hcldec.ObjectSpec { return b.config.FlatMapstructure().HCL2Spec() }

func (b *Builder) Prepare(raws ...interface{}) ([]string, error) {
	warnings, errs := b.config.Prepare(raws...)
	if errs != nil {
		return warnings, errs
	}
	return nil, nil
}

func (b *Builder) Run(ctx context.Context, ui packer.Ui, hook packer.Hook) (ret packer.Artifact, err error) {
	ui.Say("Running builder ...")

	client := newLinodeClient(b.config.PersonalAccessToken)

	if err != nil {
		ui.Error(err.Error())
		return nil, err
	}

	state := new(multistep.BasicStateBag)
	state.Put("config", b.config)
	state.Put("hook", hook)
	state.Put("ui", ui)

	steps := []multistep.Step{
		&StepCreateSSHKey{
			Debug:        b.config.PackerDebug,
			DebugKeyPath: fmt.Sprintf("linode_%s.pem", b.config.PackerBuildName),
		},
		&stepCreateLinode{client},
		&communicator.StepConnect{
			Config:    &b.config.Comm,
			Host:      commHost(b.config.Comm.SSHHost),
			SSHConfig: b.config.Comm.SSHConfigFunc(),
		},
		&common.StepProvision{},
		&common.StepCleanupTempKeys{
			Comm: &b.config.Comm,
		},
		&stepShutdownLinode{client},
		&stepCreateImage{client},
	}

	b.runner = common.NewRunner(steps, b.config.PackerConfig, ui)
	b.runner.Run(ctx, state)

	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}

	// If we were interrupted or cancelled, then just exit.
	if _, ok := state.GetOk(multistep.StateCancelled); ok {
		return nil, errors.New("Build was cancelled.")
	}

	if _, ok := state.GetOk(multistep.StateHalted); ok {
		return nil, errors.New("Build was halted.")
	}

	if _, ok := state.GetOk("image"); !ok {
		return nil, errors.New("Cannot find image in state.")
	}

	image := state.Get("image").(*linodego.Image)
	artifact := Artifact{
		ImageLabel: image.Label,
		ImageID:    image.ID,
		Driver:     &client,
	}

	return artifact, nil
}

func commHost(host string) func(multistep.StateBag) (string, error) {
	return func(state multistep.StateBag) (string, error) {
		if host != "" {
			log.Printf("Using ssh_host value: %s", host)
			return host, nil
		}

		instance := state.Get("instance").(*linodego.Instance)
		if len(instance.IPv4) == 0 {
			return "", fmt.Errorf("Linode instance %d has no IPv4 addresses!", instance.ID)
		}
		return instance.IPv4[0].String(), nil
	}
}
