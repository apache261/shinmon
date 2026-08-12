package main

import (
	"os"

	"github.com/apache261/Shinmon/internal/bootstrap"
	"github.com/apache261/Shinmon/internal/config"
)

func main() {
	os.Exit(bootstrap.Run(config.Admin))
}
