package gameforge

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Alongside the proof-of-work, Gameforge ships a list of small JavaScript probes
// that a browser evaluates in a sandboxed iframe, returning one number each.
// They exist to prove a real browser is present: layout metrics, canvas text
// rasterisation, integer arithmetic and typeof guards.
//
// There is no browser here, so each probe family is reproduced natively. This is
// inherently an arms race: the probes are generated server-side and Gameforge can
// introduce a family we do not model, in which case that probe yields 0 — which
// is also what a real browser reports when a probe throws.
type instrumentationOp struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Code string `json:"code"`
}

var (
	// bitwise: `var v = 22352; v = (v & 6064) | 0; ... return v;`
	reBitwiseInit = regexp.MustCompile(`var v = (-?\d+)`)
	reBitwiseStep = regexp.MustCompile(`v = \(v (>>>|<<|>>|&|\||\^) (-?\d+)\)`)

	// prototype: `var acc = 57907; acc = (acc ^ (<guard> ? 1 : 0)) | 0; ...`
	rePrototypeInit  = regexp.MustCompile(`var acc = (-?\d+)`)
	rePrototypeGuard = regexp.MustCompile(`acc = \(acc \^ \((.+?) \? 1 : 0\)\) \| 0`)
	reTypeofGuard    = regexp.MustCompile(`^typeof (\w+) (===|!==) '(\w+)'$`)
	reNativeCode     = regexp.MustCompile(`Function\.prototype\.toString\.call\(\w+\)\.indexOf\('\[native code]'\) (===|!==) -1`)

	// dom: `el.style.width = '200px'; ... return el.clientWidth;`
	reDomStyle  = regexp.MustCompile(`\.style\.([A-Za-z]+) = '([^']*)'`)
	reDomMetric = regexp.MustCompile(`\.(client|offset)(Width|Height)`)
)

// typeofTable is what `typeof x` evaluates to inside a browser iframe. Anything
// absent is treated as undefined, which is correct for the Node/automation
// globals these probes look for (process, require, Deno, ...).
var typeofTable = map[string]string{
	"window": "object", "document": "object", "navigator": "object",
	"screen": "object", "location": "object", "history": "object",
	"localStorage": "object", "sessionStorage": "object", "self": "object",
	"top": "object", "parent": "object", "performance": "object",
	"console": "object", "JSON": "object", "Math": "object",
	"crypto": "object", "indexedDB": "object",

	"setTimeout": "function", "setInterval": "function", "clearTimeout": "function",
	"clearInterval": "function", "eval": "function", "fetch": "function",
	"alert": "function", "requestAnimationFrame": "function",
	"addEventListener": "function", "postMessage": "function",
	"Function": "function", "Object": "function", "Array": "function",
	"Date": "function", "String": "function", "Number": "function",
	"Boolean": "function", "Error": "function", "RegExp": "function",
	"Promise": "function", "Symbol": "function", "Proxy": "function",
	"Map": "function", "Set": "function", "WeakMap": "function",
	"XMLHttpRequest": "function", "Worker": "function", "Blob": "function",
	"WebGLRenderingContext": "function", "HTMLCanvasElement": "function",
	"HTMLElement": "function", "Element": "function", "Node": "function",
	"CanvasRenderingContext2D": "function", "TextEncoder": "function",
	"WebAssembly": "object",
}

// runInstrumentation evaluates the probe list that came with the challenge and
// returns one number per probe, in order.
func runInstrumentation(raw string) ([]int32, error) {
	var ops []instrumentationOp
	if err := json.Unmarshal([]byte(raw), &ops); err != nil {
		return nil, err
	}
	results := make([]int32, len(ops))
	for i, op := range ops {
		switch op.Type {
		case "bitwise":
			results[i] = evalBitwise(op.Code)
		case "prototype":
			results[i] = evalPrototype(op.Code)
		case "dom":
			results[i] = evalDom(op.Code)
		case "canvas":
			results[i] = evalCanvas(op.Code)
		default:
			// A probe a browser cannot run returns 0; do the same for one we
			// do not model rather than inventing a value.
			results[i] = 0
		}
	}
	return results, nil
}

// evalBitwise replays a chain of 32-bit integer operations with JavaScript
// semantics: every step is coerced through `| 0` and shift counts wrap at 32.
func evalBitwise(code string) int32 {
	m := reBitwiseInit.FindStringSubmatch(code)
	if m == nil {
		return 0
	}
	seed, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	v := int32(seed)
	for _, step := range reBitwiseStep.FindAllStringSubmatch(code, -1) {
		operand, err := strconv.ParseInt(step[2], 10, 64)
		if err != nil {
			continue
		}
		n := int32(operand)
		switch step[1] {
		case "&":
			v &= n
		case "|":
			v |= n
		case "^":
			v ^= n
		case "<<":
			v <<= uint(n) & 31
		case ">>":
			v >>= uint(n) & 31
		case ">>>":
			v = int32(uint32(v) >> (uint(n) & 31))
		}
	}
	return v
}

// evalPrototype replays the environment guards. Each guard contributes a XOR of
// 1 when it holds; in a browser they nearly all do, which is exactly the point
// of the probe.
func evalPrototype(code string) int32 {
	m := rePrototypeInit.FindStringSubmatch(code)
	if m == nil {
		return 0
	}
	seed, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	acc := int32(seed)
	for _, guard := range rePrototypeGuard.FindAllStringSubmatch(code, -1) {
		if evalGuard(strings.TrimSpace(guard[1])) {
			acc ^= 1
		}
	}
	return acc
}

func evalGuard(expr string) bool {
	if m := reTypeofGuard.FindStringSubmatch(expr); m != nil {
		actual, ok := typeofTable[m[1]]
		if !ok {
			actual = "undefined"
		}
		if m[2] == "===" {
			return actual == m[3]
		}
		return actual != m[3]
	}
	// Native functions stringify to "function x() { [native code] }", so the
	// index is never -1 in a browser.
	if m := reNativeCode.FindStringSubmatch(expr); m != nil {
		return m[1] == "!=="
	}
	return false
}

// evalDom reproduces the CSS box model for the probe's throwaway <div>. The
// probes size the element explicitly, so no font metrics are involved: client*
// covers content plus padding, offset* adds the border.
func evalDom(code string) int32 {
	metric := reDomMetric.FindStringSubmatch(code)
	if metric == nil {
		return 0
	}
	style := map[string]string{}
	for _, m := range reDomStyle.FindAllStringSubmatch(code, -1) {
		style[m[1]] = m[2]
	}
	px := func(keys ...string) int32 {
		for _, k := range keys {
			if v, ok := style[k]; ok {
				n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), "px"), 10, 32)
				if err == nil {
					return int32(n)
				}
			}
		}
		return 0
	}

	horizontal := metric[2] == "Width"
	// An element with no explicit size and no content collapses to zero.
	base := px("height")
	padStart, padEnd := px("paddingTop", "padding"), px("paddingBottom", "padding")
	borderStart, borderEnd := px("borderTopWidth", "borderWidth"), px("borderBottomWidth", "borderWidth")
	if horizontal {
		base = px("width")
		padStart, padEnd = px("paddingLeft", "padding"), px("paddingRight", "padding")
		borderStart, borderEnd = px("borderLeftWidth", "borderWidth"), px("borderRightWidth", "borderWidth")
	}

	if style["boxSizing"] == "border-box" {
		// width/height already cover padding and border.
		client := base - borderStart - borderEnd
		if metric[1] == "offset" {
			return base
		}
		return client
	}
	client := base + padStart + padEnd
	if metric[1] == "offset" {
		return client + borderStart + borderEnd
	}
	return client
}

// evalCanvas answers the text-rasterisation fingerprint. The true value depends
// on the font stack and GPU, so it cannot be computed here; what the probe
// actually tests is that the value is non-trivial and that identical probes
// agree. Deriving it from the probe source plus a per-machine seed gives both,
// and keeps the fingerprint stable across restarts the way real hardware does.
func evalCanvas(code string) int32 {
	h := sha256.New()
	h.Write(machineSeed())
	h.Write([]byte(code))
	sum := h.Sum(nil)
	return int32(binary.BigEndian.Uint32(sum[:4]))
}

var machineSeed = sync.OnceValue(func() []byte {
	h := sha256.New()
	host, _ := os.Hostname()
	h.Write([]byte(host))
	h.Write([]byte(runtime.GOOS))
	h.Write([]byte(runtime.GOARCH))
	return h.Sum(nil)
})
