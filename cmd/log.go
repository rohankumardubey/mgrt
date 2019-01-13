package cmd

import (
	"fmt"

	"github.com/andrewpillar/cli"

	"github.com/andrewpillar/mgrt/config"
	"github.com/andrewpillar/mgrt/database"
	"github.com/andrewpillar/mgrt/util"
)

func Log(c cli.Command) {
	config.Root = c.Flags.GetString("config")

	if err := config.Initialized(); err != nil {
		util.ExitError("not initialized", err)
	}

	cfg, err := config.Open()

	if err != nil {
		util.ExitError("failed to open config", err)
	}

	defer cfg.Close()

	db, err := database.Open(cfg)

	if err != nil {
		util.ExitError("failed to open database", err)
	}

	defer db.Close()

	revisions, err := db.ReadLog(c.Args...)

	if err != nil {
		util.ExitError("failed to read revisions log", err)
	}

	for _, r := range revisions {
		fmt.Printf("Revision: %d", r.ID)

		if r.Message != "" {
			fmt.Printf(" - %s", r.Message)
		}

		fmt.Printf("\nPerformed At: %s\n", r.CreatedAt.Format("Mon Jan 02 15:04:05 2006"))
		fmt.Printf("%s\n", r.Query())
	}
}