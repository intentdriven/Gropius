package main

import _ "embed"

// menuIcon is a macOS *template* image: pure black with an alpha mask. macOS
// recolors it automatically for light and dark menu bars, so we must not bake
// in the Gropius colors here.
//
//go:embed icon.png
var menuIcon []byte
