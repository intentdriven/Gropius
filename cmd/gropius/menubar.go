package main

import (
	"fmt"
	"log/slog"
	"time"

	"fyne.io/systray"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/gateway"
)

// quitMenuBar tears the tray down, which makes runMenuBar return.
func quitMenuBar() { systray.Quit() }

// runMenuBar shows the menu-bar item for the primary (server) instance.
//
// It BLOCKS until the user quits, and must be called from the main goroutine:
// AppKit's event loop has to own the main thread. Calling it with `go` crashes
// with SIGTRAP inside cgo as soon as the tray initializes.
func runMenuBar(a *app.App, log *slog.Logger) {
	systray.Run(func() {
		systray.SetTemplateIcon(menuIcon, menuIcon)
		systray.SetTooltip("Gropius — MLX model server")

		status := systray.AddMenuItem("Starting…", "")
		status.Disable()
		endpoint := systray.AddMenuItem("", "The address other machines should use")
		endpoint.Disable()

		systray.AddSeparator()
		open := systray.AddMenuItem("Open Control Panel", "Manage models and settings")
		copyURL := systray.AddMenuItem("Copy Endpoint URL", "Copy the API base URL")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("Quit Gropius", "Stop the server and quit")

		// Keep the menu in step with reality: models load and unload, downloads
		// finish, the network address can change.
		go func() {
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for range tick.C {
				ready := len(a.Registry.Ready())
				loaded := len(a.Pool.Resident())

				line := fmt.Sprintf("%d model%s · %d loaded", ready, plural(ready), loaded)
				if !a.Provisioner.Installed() {
					line = "Setting up MLX runtime…"
				}
				status.SetTitle(line)

				// The first entry, as before: the private-network address is
				// marked in the panel, not promoted here. Which address the
				// menu bar hands out is behaviour, and the mark is
				// presentation.
				eps := gateway.Endpoints(a.Config(), a.Bind())
				if len(eps) > 0 {
					endpoint.SetTitle(eps[0].URL)
				}
			}
		}()

		go func() {
			for {
				select {
				case <-open.ClickedCh:
					openBrowser(panelURL(a.Config()))
				case <-copyURL.ClickedCh:
					// The URL and nothing else: a mark pasted into a client's
					// base-URL field is not a URL.
					if eps := gateway.Endpoints(a.Config(), a.Bind()); len(eps) > 0 {
						copyToClipboard(eps[0].URL)
					}
				case <-quitItem.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {
		log.Info("menu bar closed")
	})
}

// runClientMenuBar is the menu for a secondary instance, whose only job is to
// point the user at the server that is already running. Blocks; main goroutine
// only, like runMenuBar.
func runClientMenuBar(cfg config.Config) {
	systray.Run(func() {
		systray.SetTemplateIcon(menuIcon, menuIcon)
		systray.SetTooltip("Gropius — connected to the server on this Mac")

		status := systray.AddMenuItem("Server running in another account", "")
		status.Disable()
		systray.AddSeparator()
		open := systray.AddMenuItem("Open Control Panel", "")
		quitItem := systray.AddMenuItem("Quit", "")

		go func() {
			for {
				select {
				case <-open.ClickedCh:
					openBrowser(panelURL(cfg))
				case <-quitItem.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
