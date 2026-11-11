package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// The Windows launcher's colours (LauncherForm's static fields).
var (
	colBack    = color.NRGBA{10, 14, 22, 255}
	colPanel   = color.NRGBA{20, 27, 40, 255}
	colHover   = color.NRGBA{30, 40, 58, 255}
	colText1   = color.NRGBA{225, 232, 242, 255}
	colText2   = color.NRGBA{140, 155, 175, 255}
	colAccent  = color.NRGBA{232, 150, 60, 255}
	colPlayBg  = color.NRGBA{120, 70, 20, 255}
	colPlayHov = color.NRGBA{145, 86, 26, 255}
	colBlue    = color.NRGBA{70, 150, 230, 255}
	colBad     = color.NRGBA{235, 90, 80, 255}
	colBorder  = color.NRGBA{60, 75, 100, 255}
)

// launcherTheme is a dark theme in those colours, the same on every desktop.
type launcherTheme struct{ big bool }

func (t launcherTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBack
	case theme.ColorNameButton, theme.ColorNameInputBackground, theme.ColorNameMenuBackground,
		theme.ColorNameOverlayBackground, theme.ColorNameHeaderBackground:
		return colPanel
	case theme.ColorNameDisabledButton:
		return colPanel
	case theme.ColorNameForeground:
		return colText1
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return colText2
	case theme.ColorNamePrimary, theme.ColorNameWarning:
		return colAccent
	case theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnWarning:
		return colBack
	case theme.ColorNameError:
		return colBad
	case theme.ColorNameForegroundOnError:
		return colText1
	case theme.ColorNameSuccess, theme.ColorNameHyperlink:
		return colBlue
	case theme.ColorNameInputBorder, theme.ColorNameSeparator, theme.ColorNameScrollBar:
		return colBorder
	case theme.ColorNameScrollBarBackground:
		return colPanel
	case theme.ColorNameHover:
		return color.NRGBA{255, 255, 255, 18}
	case theme.ColorNamePressed:
		return color.NRGBA{255, 255, 255, 30}
	case theme.ColorNameFocus:
		return color.NRGBA{232, 150, 60, 110}
	case theme.ColorNameSelection:
		return color.NRGBA{232, 150, 60, 80}
	case theme.ColorNameShadow:
		return color.NRGBA{0, 0, 0, 120}
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (launcherTheme) Font(s fyne.TextStyle) fyne.Resource { return theme.DefaultTheme().Font(s) }

func (launcherTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

func (t launcherTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		if t.big {
			return 21
		}
		return 13
	case theme.SizeNamePadding:
		return 4
	case theme.SizeNameInnerPadding:
		return 5
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 2
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameScrollBarSmall:
		return 4
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameSubHeadingText:
		return 15
	case theme.SizeNameCaptionText:
		return 11
	}
	return theme.DefaultTheme().Size(n)
}
