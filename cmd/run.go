package cmd

import (
	"github.com/andrewpillar/cli"

	"github.com/andrewpillar/mgrt/revision"
)

func Run(c cli.Command) {
	perform(c, revision.Up)
}
