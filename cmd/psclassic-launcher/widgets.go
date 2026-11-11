package main

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/ltsjoe/psclassic-launcher/internal/psc"
)

// place puts an object at an absolute position, as the Windows launcher lays
// out its windows with SetBounds.
func place(o fyne.CanvasObject, x, y, w, h float32) fyne.CanvasObject {
	o.Move(fyne.NewPos(x, y))
	o.Resize(fyne.NewSize(w, h))
	return o
}

func newText(s string, c color.Color, size float32, bold bool) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle.Bold = bold
	return t
}

// label is a one-line text at (x, y) that sizes itself.
func label(s string, x, y float32) fyne.CanvasObject {
	t := newText(s, colText1, 13, false)
	t.Move(fyne.NewPos(x, y))
	t.Resize(t.MinSize())
	return t
}

func heading(s string, x, y float32) fyne.CanvasObject {
	t := newText(s, colText1, 14, true)
	t.Move(fyne.NewPos(x, y))
	t.Resize(t.MinSize())
	return t
}

// note is wrapped help text in the second text colour.
func note(s string, imp widget.Importance) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = imp
	return l
}

// flatButton is the launcher's flat button: a filled box with a 1px border.
// "dim" draws the text in the second colour (the launcher does that while busy;
// the button still takes clicks). An accent button has an orange border, and
// an orange-brown fill while it is active (PLAY when the game can start).
type flatButton struct {
	widget.BaseWidget
	text     string
	accent   bool
	active   bool // accent buttons: filled; others: ignored
	dim      bool
	textSize float32
	bold     bool
	hovered  bool
	onTap    func()
}

func newFlatButton(text string, accent bool, onTap func()) *flatButton {
	b := &flatButton{text: text, accent: accent, onTap: onTap, textSize: 13}
	b.ExtendBaseWidget(b)
	return b
}

func (b *flatButton) SetText(s string) { b.text = s; b.Refresh() }
func (b *flatButton) SetDim(d bool)    { b.dim = d; b.Refresh() }
func (b *flatButton) SetActive(a bool) { b.active = a; b.Refresh() }

func (b *flatButton) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *flatButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.Refresh() }
func (b *flatButton) MouseMoved(*desktop.MouseEvent) {}
func (b *flatButton) MouseOut()                      { b.hovered = false; b.Refresh() }
func (b *flatButton) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (b *flatButton) CreateRenderer() fyne.WidgetRenderer {
	r := &flatButtonRenderer{b: b, rect: canvas.NewRectangle(colPanel), text: canvas.NewText("", colText1)}
	r.rect.StrokeWidth = 1
	r.Refresh()
	return r
}

type flatButtonRenderer struct {
	b    *flatButton
	rect *canvas.Rectangle
	text *canvas.Text
}

func (r *flatButtonRenderer) Layout(s fyne.Size) {
	r.rect.Resize(s)
	ts := r.text.MinSize()
	r.text.Move(fyne.NewPos((s.Width-ts.Width)/2, (s.Height-ts.Height)/2))
	r.text.Resize(ts)
}

func (r *flatButtonRenderer) MinSize() fyne.Size {
	ts := r.text.MinSize()
	return fyne.NewSize(ts.Width+16, ts.Height+8)
}

func (r *flatButtonRenderer) Refresh() {
	b := r.b
	fill, border := color.Color(colPanel), color.Color(colBorder)
	if b.hovered {
		fill = colHover
	}
	if b.accent {
		border = colAccent
		if b.active {
			fill = colPlayBg
			if b.hovered {
				fill = colPlayHov
			}
		}
	}
	textCol := color.Color(colText1)
	if b.dim || (b.accent && !b.active) {
		textCol = colText2
	}
	r.rect.FillColor, r.rect.StrokeColor = fill, border
	r.text.Text, r.text.Color, r.text.TextSize = b.text, textCol, b.textSize
	r.text.TextStyle.Bold = b.bold
	r.Layout(b.Size())
	r.rect.Refresh()
	r.text.Refresh()
}

func (r *flatButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.rect, r.text}
}
func (r *flatButtonRenderer) Destroy() {}

// progressBar is the thin bar under the status line.
type progressBar struct {
	widget.BaseWidget
	frac float64
}

func newProgressBar() *progressBar {
	p := &progressBar{}
	p.ExtendBaseWidget(p)
	return p
}

// Set shows done of total (the launcher's SetProgress).
func (p *progressBar) Set(done, total int64) {
	f := 0.0
	if total > 0 {
		f = min(1, float64(done)/float64(total))
	}
	if f != p.frac {
		p.frac = f
		p.Refresh()
	}
}

func (p *progressBar) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(colPanel)
	bg.StrokeColor, bg.StrokeWidth = colBorder, 1
	fill := canvas.NewRectangle(colAccent)
	r := &progressRenderer{p: p, bg: bg, fill: fill}
	return r
}

type progressRenderer struct {
	p        *progressBar
	bg, fill *canvas.Rectangle
}

func (r *progressRenderer) Layout(s fyne.Size) {
	r.bg.Resize(s)
	r.fill.Move(fyne.NewPos(1, 1))
	r.fill.Resize(fyne.NewSize(float32(r.p.frac)*(s.Width-2), s.Height-2))
}
func (r *progressRenderer) MinSize() fyne.Size           { return fyne.NewSize(10, 6) }
func (r *progressRenderer) Refresh()                     { r.Layout(r.p.Size()); canvas.Refresh(r.p) }
func (r *progressRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.fill} }
func (r *progressRenderer) Destroy()                     {}

// keyPicker is the launcher's key button: click it, then press a key. Fyne does
// not report the side mouse buttons, so a right-click offers the mouse buttons.
type keyPicker struct {
	widget.BaseWidget
	key     string
	waiting bool
	btn     *flatButton
	canvas  func() fyne.Canvas
}

func newKeyPicker(key string, c func() fyne.Canvas) *keyPicker {
	k := &keyPicker{key: key, canvas: c}
	k.btn = newFlatButton(psc.KeyDisplay(key), false, k.start)
	k.ExtendBaseWidget(k)
	return k
}

// Key is the chosen key in the launcher's stored form ("Y", "VK120", "Mouse4").
func (k *keyPicker) Key() string { return k.key }

func (k *keyPicker) start() {
	k.waiting = true
	k.btn.SetText("Press a key (Esc = none, right-click: mouse)")
	k.btn.textSize = 11
	k.btn.Refresh()
	if c := k.canvas(); c != nil {
		c.Focus(k)
	}
}

func (k *keyPicker) set(key string) {
	k.key = key
	k.stop()
}

func (k *keyPicker) stop() {
	k.waiting = false
	k.btn.textSize = 13
	k.btn.SetText(psc.KeyDisplay(k.key))
}

func (k *keyPicker) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(k.btn) }

func (k *keyPicker) FocusGained() {}
func (k *keyPicker) FocusLost() {
	if k.waiting {
		k.stop()
	}
}
func (k *keyPicker) TypedRune(rune)              {}
func (k *keyPicker) TypedKey(*fyne.KeyEvent)     {}
func (k *keyPicker) AcceptsTab() bool            { return k.waiting }
func (k *keyPicker) KeyUp(*fyne.KeyEvent)        {}
func (k *keyPicker) MouseUp(*desktop.MouseEvent) {}

func (k *keyPicker) KeyDown(ev *fyne.KeyEvent) {
	if !k.waiting {
		return
	}
	if ev.Name == fyne.KeyEscape {
		k.set("None")
		return
	}
	if vk, ok := fyneVK[ev.Name]; ok {
		k.set(psc.FromKeyNumber(vk))
	}
}

func (k *keyPicker) MouseDown(ev *desktop.MouseEvent) {
	if k.waiting && ev.Button == desktop.MouseButtonTertiary {
		k.set("Mouse3")
	}
}

func (k *keyPicker) TappedSecondary(ev *fyne.PointEvent) {
	c := k.canvas()
	if c == nil {
		return
	}
	item := func(key string) *fyne.MenuItem {
		return fyne.NewMenuItem(psc.KeyDisplay(key), func() { k.set(key) })
	}
	menu := fyne.NewMenu("", item("Mouse3"), item("Mouse4"), item("Mouse5"), item("None"))
	widget.ShowPopUpMenuAtPosition(menu, c, ev.AbsolutePosition)
}

// fyneVK maps Fyne's key names to Windows virtual-key numbers. Shift, Ctrl and
// Alt map to VK_SHIFT, VK_CONTROL and VK_MENU, as the Windows launcher's key
// button stores them.
var fyneVK = map[fyne.KeyName]int{
	fyne.KeySpace: 0x20, fyne.KeyTab: 0x09, fyne.KeyReturn: 0x0D, fyne.KeyEnter: 0x0D, fyne.KeyBackspace: 0x08,
	fyne.KeyInsert: 0x2D, fyne.KeyDelete: 0x2E, fyne.KeyHome: 0x24, fyne.KeyEnd: 0x23,
	fyne.KeyPageUp: 0x21, fyne.KeyPageDown: 0x22,
	fyne.KeyLeft: 0x25, fyne.KeyUp: 0x26, fyne.KeyRight: 0x27, fyne.KeyDown: 0x28,
	desktop.KeyShiftLeft: 0x10, desktop.KeyShiftRight: 0x10,
	desktop.KeyControlLeft: 0x11, desktop.KeyControlRight: 0x11,
	desktop.KeyAltLeft: 0x12, desktop.KeyAltRight: 0x12,
	desktop.KeyCapsLock: 0x14, desktop.KeyPrintScreen: 0x2C,
	fyne.KeySemicolon: 0xBA, fyne.KeyEqual: 0xBB, fyne.KeyComma: 0xBC, fyne.KeyMinus: 0xBD,
	fyne.KeyPeriod: 0xBE, fyne.KeySlash: 0xBF, fyne.KeyBackTick: 0xC0,
	fyne.KeyLeftBracket: 0xDB, fyne.KeyBackslash: 0xDC, fyne.KeyRightBracket: 0xDD, fyne.KeyApostrophe: 0xDE,
	fyne.KeyAsterisk: 0x6A, fyne.KeyPlus: 0x6B,
}

func init() {
	for c := 'A'; c <= 'Z'; c++ {
		fyneVK[fyne.KeyName(string(c))] = int(c)
	}
	for c := '0'; c <= '9'; c++ {
		fyneVK[fyne.KeyName(string(c))] = int(c)
	}
	for i := 1; i <= 12; i++ {
		fyneVK[fyne.KeyName("F"+strconv.Itoa(i))] = 0x6F + i
	}
}

// spinLayout puts an entry and two small arrow buttons side by side, like a
// NumericUpDown.
type spinLayout struct{}

func (spinLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	const bw = 20
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(s.Width-bw-2, s.Height))
	objs[1].Move(fyne.NewPos(s.Width-bw, 0))
	objs[1].Resize(fyne.NewSize(bw, s.Height/2))
	objs[2].Move(fyne.NewPos(s.Width-bw, s.Height/2))
	objs[2].Resize(fyne.NewSize(bw, s.Height/2))
}

func (spinLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(60, 26) }

// spinner is a number field with up/down buttons, limited to [lo, hi].
type spinner struct {
	entry  *widget.Entry
	lo, hi int
	box    *fyne.Container
}

func newSpinner(value, lo, hi, step int) *spinner {
	s := &spinner{entry: widget.NewEntry(), lo: lo, hi: hi}
	s.entry.SetText(strconv.Itoa(value))
	bump := func(d int) func() {
		return func() {
			v, _ := s.Value()
			s.entry.SetText(strconv.Itoa(max(lo, min(hi, v+d))))
		}
	}
	up := newFlatButton("▴", false, bump(step))
	down := newFlatButton("▾", false, bump(-step))
	up.textSize, down.textSize = 10, 10
	s.box = newBox(spinLayout{}, s.entry, up, down)
	return s
}

// Value is the number in the field, clamped; ok is false when it is not a number.
func (s *spinner) Value() (int, bool) {
	n, err := strconv.Atoi(s.entry.Text)
	if err != nil {
		return s.lo, false
	}
	return max(s.lo, min(s.hi, n)), true
}

// newBox makes a container; a nil layout leaves objects where place put them.
func newBox(l fyne.Layout, objs ...fyne.CanvasObject) *fyne.Container {
	return &fyne.Container{Layout: l, Objects: objs}
}
