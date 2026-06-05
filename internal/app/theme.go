package app

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Semantic palette — the single source of truth for colors. Everything else in
// the app derives from these named colors rather than ad-hoc hex literals.
const (
	colBg          = "#0B1220" // base panel background
	colBgAlt       = "#0F1A2E" // alternate row tint
	colInk         = "#D7DEE9" // default foreground
	colInkBright   = "#F8FAFC" // bright/emphasis foreground
	colInkInverse  = "#0F172A" // foreground on bright backgrounds
	colBorder      = "#334155" // idle panel border
	colMuted       = "#94A3B8" // dimmed text / placeholders
	colAccent      = "#38BDF8" // primary accent (cyan-blue)
	colInfo        = "#60A5FA"
	colSuccess     = "#4ADE80"
	colWarning     = "#FBBF24"
	colDanger      = "#F87171"
	colDangerLine  = "#EF4444"
	colViolet      = "#A78BFA"
	colHeaderBg    = "#1D4ED8"
	colStatusBg    = "#111827"
	colFooterBg    = "#1E293B"
	colSelectBg    = "#67E8F9" // selected row / cursor-in-list
	colCursorBg    = "#14532D" // blinking character cursor
	colVisualBg    = "#2563EB" // visual selection
	colKey         = "#FDE68A" // keyboard hints
	colSidebarBg   = "#0A1F2E"
	colSidebarEdge = "#0E7490"
	colMainBg      = "#0B1F16"
	colMainEdge    = "#166534"
	colSidebarFocusBg   = "#083344"
	colSidebarFocusEdge = "#22D3EE"
	colMainFocusBg      = "#052E16"
	colMainFocusEdge    = "#86EFAC"
	colQueryInputBg     = "#0F2A1C"
	colTabQuery         = "#FDE68A"
	colTabData          = "#A7F3D0"
	colTabActiveBg      = "#FDE047"
	colModalBg          = "#111827"
	colHistoryBg        = "#241F36"
)

type appTheme struct {
	header     lipgloss.Style
	status     lipgloss.Style
	panel      lipgloss.Style
	sidebar    lipgloss.Style
	main       lipgloss.Style
	context    lipgloss.Style
	focused    lipgloss.Style
	panelTitle lipgloss.Style
	muted      lipgloss.Style
	accent     lipgloss.Style
	danger     lipgloss.Style
	footer     lipgloss.Style
	command    lipgloss.Style
	selected   lipgloss.Style
	cursor     lipgloss.Style
	visual     lipgloss.Style
	key        lipgloss.Style
	queryInput lipgloss.Style
	resultBar  lipgloss.Style
	tableHead  lipgloss.Style

	// Restyle additions.
	sectionTitle lipgloss.Style // modal / help group titles
	statusOK     lipgloss.Style
	statusWarn   lipgloss.Style
	statusErr    lipgloss.Style
	statusInfo   lipgloss.Style
	badge        lipgloss.Style // generic badge (type)
	badgeRO      lipgloss.Style // read-only badge
	badgeRW      lipgloss.Style
	button       lipgloss.Style
	buttonDanger lipgloss.Style
	scrollbar    lipgloss.Style
	hintKey      lipgloss.Style
	hintLabel    lipgloss.Style
	rowAlt       lipgloss.Style
	spinner      lipgloss.Style

	// Centralized panel focus colors (formerly inline literals).
	sidebarFocusBg, sidebarFocusEdge lipgloss.Color
	mainFocusBg, mainFocusEdge       lipgloss.Color
	// Centralized accent colors used outside style structs.
	accentColor, violetColor, dangerColor, modalBgColor, historyBgColor lipgloss.Color
	tabQueryColor, tabDataColor, tabActiveBg, tabActiveFg               lipgloss.Color
}

func defaultTheme() appTheme {
	lipgloss.SetColorProfile(termenv.TrueColor)
	return appTheme{
		header: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#E8EEF7")).
			Background(lipgloss.Color(colHeaderBg)).
			Padding(0, 1),
		status: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C7D2FE")).
			Background(lipgloss.Color(colStatusBg)).
			Padding(0, 1),
		panel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBorder)).
			Foreground(lipgloss.Color(colInk)).
			Background(lipgloss.Color(colBg)).
			Padding(0, 1),
		sidebar: lipgloss.NewStyle().BorderForeground(lipgloss.Color(colSidebarEdge)).Background(lipgloss.Color(colSidebarBg)),
		main:    lipgloss.NewStyle().BorderForeground(lipgloss.Color(colMainEdge)).Background(lipgloss.Color(colMainBg)),
		context: lipgloss.NewStyle().BorderForeground(lipgloss.Color(colWarning)),
		focused: lipgloss.NewStyle().
			Bold(true).
			BorderForeground(lipgloss.Color(colTabActiveBg)).
			Foreground(lipgloss.Color(colInkBright)).
			Background(lipgloss.Color("#172554")),
		panelTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colInkBright)),
		muted:  lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
		accent: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)),
		danger: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colDanger)).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colDangerLine)).
			Padding(0, 1),
		footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CBD5E1")).
			Background(lipgloss.Color(colFooterBg)).
			Padding(0, 1),
		command: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colKey)).
			Background(lipgloss.Color(colStatusBg)).
			Padding(0, 1),
		selected: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colInkInverse)).
			Background(lipgloss.Color(colSelectBg)),
		cursor: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colInkBright)).
			Background(lipgloss.Color(colCursorBg)),
		visual: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colInkBright)).
			Background(lipgloss.Color(colVisualBg)),
		key:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colKey)),
		queryInput: lipgloss.NewStyle().Foreground(lipgloss.Color("#E2E8F0")).Background(lipgloss.Color(colQueryInputBg)),
		resultBar: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colInkInverse)).
			Background(lipgloss.Color(colAccent)).
			Padding(0, 1),
		tableHead: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colInkInverse)).
			Background(lipgloss.Color(colMuted)),

		sectionTitle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)),
		statusOK:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colSuccess)),
		statusWarn:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colWarning)),
		statusErr:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colDanger)),
		statusInfo:   lipgloss.NewStyle().Foreground(lipgloss.Color("#CBD5E1")),
		badge:        lipgloss.NewStyle().Foreground(lipgloss.Color(colInkInverse)).Background(lipgloss.Color(colMuted)),
		badgeRO:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colInkInverse)).Background(lipgloss.Color(colWarning)),
		badgeRW:      lipgloss.NewStyle().Foreground(lipgloss.Color(colInkInverse)).Background(lipgloss.Color(colSuccess)),
		button:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colInkInverse)).Background(lipgloss.Color(colAccent)).Padding(0, 1),
		buttonDanger: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colInkBright)).Background(lipgloss.Color(colDangerLine)).Padding(0, 1),
		scrollbar:    lipgloss.NewStyle().Foreground(lipgloss.Color(colBorder)),
		hintKey:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colKey)),
		hintLabel:    lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
		rowAlt:       lipgloss.NewStyle().Background(lipgloss.Color(colBgAlt)),
		spinner:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)),

		sidebarFocusBg:   lipgloss.Color(colSidebarFocusBg),
		sidebarFocusEdge: lipgloss.Color(colSidebarFocusEdge),
		mainFocusBg:      lipgloss.Color(colMainFocusBg),
		mainFocusEdge:    lipgloss.Color(colMainFocusEdge),
		accentColor:      lipgloss.Color(colAccent),
		violetColor:      lipgloss.Color(colViolet),
		dangerColor:      lipgloss.Color(colDangerLine),
		modalBgColor:     lipgloss.Color(colModalBg),
		historyBgColor:   lipgloss.Color(colHistoryBg),
		tabQueryColor:    lipgloss.Color(colTabQuery),
		tabDataColor:     lipgloss.Color(colTabData),
		tabActiveBg:      lipgloss.Color(colTabActiveBg),
		tabActiveFg:      lipgloss.Color(colInkInverse),
	}
}
