package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDecisionData(t *testing.T) {
	for _, approve := range []bool{true, false} {
		data := decisionData(approve, 9223372036854775807)
		if len(data) > 64 {
			t.Fatalf("callback data too long: %d", len(data))
		}
		if got, id, ok := parseDecision(data); !ok || got != approve || id != 9223372036854775807 {
			t.Fatalf("round trip %q: %v %d %v", data, got, id, ok)
		}
	}
	for _, bad := range []string{"", "acc:req", "acc:ok", "acc:ok:", "acc:ok:x", "acc:ok:-1", "acc:yes:1", "acc:ok:1:2", "accept:1"} {
		if _, _, ok := parseDecision(bad); ok {
			t.Errorf("parseDecision(%q) should fail", bad)
		}
	}
}

func TestAccessRequestTextEscapes(t *testing.T) {
	got := accessRequestText(tgUser{ID: 42, FirstName: "A<b>&", LastName: "C", Username: "x_y"})
	for _, want := range []string{`<a href="tg://user?id=42">A&lt;b&gt;&amp; C</a>`, "\n@x_y", "<code>ID 42</code>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(accessRequestText(tgUser{ID: 1, FirstName: "A"}), "@") {
		t.Error("no username line expected")
	}
}

func TestLoadConfigAdmins(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("S3_BUCKET", "b")
	t.Setenv("ALLOWED_USER_IDS", "1, 2")
	t.Setenv("ADMIN_USER_IDS", "")
	c, err := loadConfig()
	if err != nil || len(c.admins) != 2 || !c.admins[1] || !c.admins[2] {
		t.Fatalf("default admins: %v %v", c.admins, err)
	}
	t.Setenv("ADMIN_USER_IDS", " 2,3,")
	c, err = loadConfig()
	if err != nil || len(c.admins) != 2 || !c.admins[2] || !c.admins[3] || c.admins[1] {
		t.Fatalf("explicit admins: %v %v", c.admins, err)
	}
	for _, bad := range []string{"x", "0", "-5"} {
		t.Setenv("ADMIN_USER_IDS", bad)
		if _, err := loadConfig(); err == nil {
			t.Errorf("ADMIN_USER_IDS=%q should fail", bad)
		}
	}
}

func TestSendAccessRequest(t *testing.T) {
	for _, tc := range []struct {
		name, photos string
		failPhoto    bool
		want         []string
	}{
		{"photo", `{"photos":[[{"file_id":"small"},{"file_id":"big"}]]}`, false, []string{"getUserProfilePhotos", "sendPhoto"}},
		{"no photo", `{"photos":[]}`, false, []string{"getUserProfilePhotos", "sendMessage"}},
		{"photo send fails", `{"photos":[[{"file_id":"big"}]]}`, true, []string{"getUserProfilePhotos", "sendPhoto", "sendMessage"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var methods []string
			a := &app{apiURL: "https://telegram.test/", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				method := strings.TrimPrefix(r.URL.Path, "/")
				methods = append(methods, method)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				reply := `{"ok":true,"result":true}`
				switch method {
				case "getUserProfilePhotos":
					reply = `{"ok":true,"result":` + tc.photos + `}`
				case "sendPhoto":
					if body["photo"] != "big" || body["caption"] == nil || body["parse_mode"] != "HTML" || body["reply_markup"] == nil {
						t.Errorf("bad sendPhoto: %v", body)
					}
					if tc.failPhoto {
						reply = `{"ok":false,"description":"boom"}`
					}
				case "sendMessage":
					if body["text"] == nil || body["parse_mode"] != "HTML" || body["photo"] != nil || body["reply_markup"] == nil {
						t.Errorf("bad sendMessage: %v", body)
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(reply)), Header: make(http.Header)}, nil
			})}}
			if err := a.sendAccessRequest(context.Background(), 7, tgUser{ID: 42, FirstName: "A"}); err != nil {
				t.Fatal(err)
			}
			if strings.Join(methods, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("calls %v, want %v", methods, tc.want)
			}
		})
	}
}
