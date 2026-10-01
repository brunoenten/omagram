package main

import (
	"log"
	"os"

	"gioui.org/app"

	"github.com/brunoenten/omagram/internal/config"
	"github.com/brunoenten/omagram/internal/ui"
)

func main() {
	cfg := config.Load()
	go func() {
		if err := ui.Run(cfg); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
}
