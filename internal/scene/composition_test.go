package scene

import (
	"encoding/json"
	"testing"
)

func TestCompositionRejectsInvalidAuthoring(t *testing.T) {
	p := &Project{Dir: t.TempDir()}
	cases := []string{
		`{"nodes":[]}`,
		`{"nodes":[{"tag":"script","text":"alert(1)"}]}`,
		`{"nodes":[{"attributes":{"onclick":"alert(1)"}}]}`,
		`{"nodes":[{"attributes":{"href":"https://example.com"}}]}`,
		`{"nodes":[{"tag":"img","attributes":{"src":"../outside.png"}}]}`,
		`{"nodes":[{"tag":"#text","text":"x","start":1}]}`,
		`{"nodes":[{"text":"x","children":[{}]}]}`,
		`{"nodes":[{"start":2,"end":1}]}`,
		`{"nodes":[{"animate":[{"property":"opacity","keyframes":[{"at":1,"value":"0"},{"at":1,"value":"1"}]}]}]}`,
		`{"nodes":[{"animate":[{"property":"opacity","keyframes":[{"at":0,"value":"0","ease":"unknown"},{"at":1,"value":"1"}]}]}]}`,
	}
	for _, body := range cases {
		var c Composition
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			t.Fatal(err)
		}
		c.Width, c.Height = 160, 90
		s := Scene{Type: "visual", Duration: 2, Composition: &c}
		if err := s.Validate(p); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	c := &Composition{Width: 160, Height: 90, Nodes: []VisualNode{{Text: "Ready"}}}
	for _, s := range []Scene{{Type: "visual", Duration: 2, Entry: "index.html", Composition: c}, {Type: "recording", Composition: c}} {
		if err := s.Validate(p); err == nil {
			t.Errorf("accepted conflicting scene: %+v", s)
		}
	}
}
