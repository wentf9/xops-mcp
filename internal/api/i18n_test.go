package api

import (
	"net/http/httptest"
	"testing"
)

func TestManagementLocale(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"", "zh-CN"}, {"en-US,en;q=0.9", "en"}, {"zh-TW,en;q=0.9", "zh-CN"},
		{"fr,en;q=0.9,zh;q=0.5", "en"}, {"en;q=0,zh;q=0.5", "zh-CN"},
		{"en;q=NaN,zh;q=0.5", "zh-CN"}, {"en;q=2", "zh-CN"},
		{"*;q=1,en;q=0.5", "zh-CN"}, {"fr-FR", "zh-CN"},
	} {
		t.Run(tc.header, func(t *testing.T) {
			if got := managementLocale(tc.header); got != tc.want {
				t.Fatalf("locale=%q want %q", got, tc.want)
			}
		})
	}
}
func TestLocalizedMessageLeavesUserDataUntouched(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Language", "en")
	if got := localizedMessage(w, "请先登录"); got != "Sign in first" {
		t.Fatal(got)
	}
	raw := `node "中文节点" has missing host or identity`
	if got := localizedMessage(w, raw); got != raw {
		t.Fatalf("user data changed: %q", got)
	}
	if got := localizedMessage(w, "执行登录身份检查失败: context deadline exceeded"); got != "Login identity check failed: context deadline exceeded" {
		t.Fatal(got)
	}
	w.Header().Set("Content-Language", "zh-CN")
	if got := localizedMessage(w, "请先登录"); got != "请先登录" {
		t.Fatal(got)
	}
}
