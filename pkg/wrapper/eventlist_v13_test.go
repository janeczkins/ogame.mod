package wrapper

import (
	"bytes"
	"testing"
)

// OGame 13 answers the event list with the markup wrapped in JSON. Before this was unwrapped the
// escaped quotes hid div#eventListWrap from goquery and the extractor reported "not logged", so
// the Defender never saw an incoming attack.
func TestUnwrapEventListEnvelope(t *testing.T) {
	v13 := []byte(`{"content":{"eventlist":"    <div id=\"eventListWrap\">\n<table id=\"eventContent\"><tbody>\n<tr class=\"eventFleet\" id=\"eventRow-128582368\"></tr>\n</tbody></table></div>"},"status":"ok"}`)

	out, ok := unwrapEventListEnvelope(v13)
	if !ok {
		t.Fatal("v13 envelope was not recognised")
	}
	if !bytes.Contains(out, []byte(`id="eventListWrap"`)) {
		t.Fatalf("unwrapped markup still has no real eventListWrap attribute: %s", out)
	}
	if !bytes.Contains(out, []byte(`id="eventRow-128582368"`)) {
		t.Fatalf("event row lost while unwrapping: %s", out)
	}

	// Plain HTML (every server before 13) must pass straight through.
	html := []byte(`<div id="eventListWrap"><table><tbody></tbody></table></div>`)
	if _, ok := unwrapEventListEnvelope(html); ok {
		t.Fatal("plain HTML must not be treated as a JSON envelope")
	}
	if _, ok := unwrapEventListEnvelope([]byte(`{"content":{"somethingelse":"x"}}`)); ok {
		t.Fatal("unrelated JSON must not be treated as an event list")
	}
	if _, ok := unwrapEventListEnvelope(nil); ok {
		t.Fatal("empty body must not be treated as a JSON envelope")
	}
}

// The guard that decides whether the extractor's wrapper must be re-added has to look at the real
// attribute: the escaped JSON also contains the bare word "eventListWrap", so a substring test
// skipped the repair on exactly the bodies that carried events.
func TestEventListWrapGuardIgnoresEscapedJSON(t *testing.T) {
	escaped := []byte(`{"content":{"eventlist":"<div id=\"eventListWrap\">"}}`)
	if bytes.Contains(escaped, []byte(`id="eventListWrap"`)) {
		t.Fatal("escaped JSON must not satisfy the real-attribute guard")
	}
	if !bytes.Contains(escaped, []byte("eventListWrap")) {
		t.Fatal("sanity: the bare word IS present - that is why the old guard misfired")
	}
}
