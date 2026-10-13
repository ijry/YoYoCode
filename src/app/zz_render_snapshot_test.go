package app

// zz_render_snapshot_test.go is a temporary local aid: it renders the shell to a
// PNG so the layout can be eyeballed against the upstream screenshots.

import (
	"encoding/json"
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestZZRenderSnapshot(t *testing.T) {
	out := os.Getenv("YOYOCODE_SNAPSHOT")
	if out == "" {
		t.Skip("set YOYOCODE_SNAPSHOT=<path.png> to render")
	}
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["folders"] = json.RawMessage(`[
		{"id":1,"name":"ai-switch","path":"/d/Repos/xyito/open/ai-switch"},
		{"id":2,"name":"ai-switch-app","path":"/d/Repos/xyito/open/ai-switch-app"},
		{"id":3,"name":"xystack","path":"/d/Repos/xyito/open/xystack"},
		{"id":4,"name":"mcode","path":"/d/Repos/xyito/lingyun/mcode"},
		{"id":5,"name":"codeg-plus","path":"/d/Repos/xyito/lingyun/codeg-plus"},
		{"id":6,"name":"lyshop","path":"/d/Repos/xyito/open/lyshop"},
		{"id":7,"name":"up-diy-view","path":"/d/Repos/xyito/open/up-diy-view"},
		{"id":8,"name":"child-story","path":"/d/Repos/xyito/open/child-story"}
	]`)
	s.cache["convs:1"] = json.RawMessage(`[
		{"id":1,"title":"Windows上（本机）还是存在不知...","agent_type":"claude_code","status":"completed","pinned":true},
		{"id":2,"title":"完善组件文档并增加示例代码","agent_type":"claude_code","status":"completed","pinned":true},
		{"id":3,"title":"[Fork] 跨平台 UPImage 组件 API ...","agent_type":"codex","status":"pending_review","pinned":true},
		{"id":4,"title":"PPT 编辑器主题重置","agent_type":"claude_code","status":"completed"}
	]`)
	s.selectedFolder = "1"
	tt := ui.NewTester(s.View, 1240, 820)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
