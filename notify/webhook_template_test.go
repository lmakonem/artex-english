package notify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This file locks in the **capability boundary** of the generic Webhook template.
//
// This is the only place in the whole package where a user-provided string is
// evaluated as code, so it must be explicit about what it can and cannot do, with
// tests pinning these properties down -- otherwise someone later casually adds a method
// to the template context, or a readFile to the FuncMap, and the capability surface
// silently expands while the diff looks like a harmless little function.

// TestTemplateContextHasNoMethods is the most important one.
//
// text/template calls exported methods ({{.Foo}} can fetch a field or call a method).
// So the moment the template context can reach **any** type with exported methods,
// those methods are exposed to the template author. This feature's context is
// deliberately all pure data (exported fields only, zero methods).
//
// If this fails: it means someone added a method to webhookTemplateData / webhookItem.
// Before deciding to allow it, think through whether that method can be used by the
// template to read something it should not.
func TestTemplateContextHasNoMethods(t *testing.T) {
	for _, v := range []any{webhookTemplateData{}, webhookItem{}} {
		typ := reflect.TypeOf(v)
		if n := typ.NumMethod(); n != 0 {
			var names []string
			for i := 0; i < n; i++ {
				names = append(names, typ.Method(i).Name)
			}
			t.Fatalf("%s exposed %d methods (%s): text/template can call them, "+
				"which opens those methods' capabilities to the template author", typ.Name(), n, strings.Join(names, ", "))
		}
	}
}

// TestTemplateFuncsAreMinimal locks in the set of functions exposed to the template.
//
// Every extra function in the FuncMap is one more capability. Currently only json /
// jsons, whose purpose is to serialize a value to a JSON fragment -- it cannot read
// files, make requests, or run commands.
func TestTemplateFuncsAreMinimal(t *testing.T) {
	var got []string
	for name := range webhookTemplateFuncs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"json", "jsons"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the template function set changed: got %v, expected %v. Before adding a function, confirm it does not expand the capability surface "+
			"(cannot read/write files, cannot make network requests, cannot run commands)", got, want)
	}
}

// TestTemplateCannotReachUnknownData covers out-of-bounds access in the template:
// accessing something that does not exist must fail rather than echo something, and the
// failure message must not carry internal data out.
func TestTemplateCannotReachUnknownData(t *testing.T) {
	_, err := renderWebhookBody(`{"x": {{.Environment}}, "y": {{.Env}}}`, singleMsg())
	if err == nil {
		t.Fatal("accessing a nonexistent field should error")
	}
	// The error must not contain the real content of the template context (finding
	// title/summary).
	for _, leak := range []string{"SQL injection", "parameter id"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the template error leaked message content %q: %v", leak, err)
		}
	}
}

// TestTemplateRenderFailsPermanently: a template typo is a config error that retrying
// will not heal. If judged retryable, one bad template would waste three rounds of
// backoff on every delivery.
func TestTemplateRenderFailsPermanently(t *testing.T) {
	cfg := map[string]any{
		"url":           "https://example.com/hook",
		"body_template": `{{.Items.`,
	}
	if err := (webhookChannel{}).Validate(cfg); err == nil {
		t.Fatal("a template syntax error should be blocked at save time")
	}
	// Even if validation is bypassed and it is delivered directly, it must be judged
	// permanent rather than retried repeatedly.
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("a bad template should be judged permanent, got %v", err)
	}
}

// TestTemplateCanOnlyProduceJSON covers the "template render result must be valid JSON"
// constraint. It also blocks the "use a template to produce plain text to trigger
// another protocol" usage.
func TestTemplateCanOnlyProduceJSON(t *testing.T) {
	// A valid template passes.
	ok := map[string]any{"url": "https://example.com/hook", "body_template": `{"t":{{json .Title}}}`}
	if err := (webhookChannel{}).Validate(ok); err != nil {
		t.Fatalf("a valid template should pass validation: %v", err)
	}
	// A non-JSON render must be rejected (rather than sent as-is).
	bad := map[string]any{"url": "http://127.0.0.1:1/hook", "body_template": `not json {{.Count}}`}
	_, err := (webhookChannel{}).Send(context.Background(), bad, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("a non-JSON render should be judged permanent, got %v", err)
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("the error message should state it is a JSON problem, got %v", err)
	}
}
