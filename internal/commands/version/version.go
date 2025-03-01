// Created By Mike Nestor <me@mikenestor.org>
package version

import (
	"fmt"

	"github.com/urfave/cli/v3"
)

func VersionPrinter(cmd *cli.Command) {
	fmt.Fprintf(cmd.Root().Writer, "version=%s\n", cmd.Root().Version)
}
