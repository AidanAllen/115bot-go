package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// mockFilesServer 按 offset 返回不同内容，用于验证 ListDirs 的跨页行为。
func mockFilesServer(t *testing.T, byOffset map[int]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		off := 0
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				off = n
			}
		}
		if body, ok := byOffset[off]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"state":false,"error":"unexpected offset"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newFilesClient(t *testing.T, base string) *Client {
	t.Helper()
	return newWithEndpoints("USERSESSIONID=test; UID=1", "test-ua", 5*time.Second, testEndpoints(base))
}

// jsonFiles 生成 n 个文件条目（有 fid 无 cid）。
func jsonFiles(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"fid":"f%d","n":"v%d.mp4","s":1024,"cid":""}`, i, i)
	}
	return b.String()
}

// jsonDirs 生成若干目录条目（有 cid 无 fid）。
func jsonDirs(names ...string) string {
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"cid":"cid-%s","n":"%s","s":0}`, n, n)
	}
	return b.String()
}

// TestListDirsFiltersFiles 文件与目录混在同一页时，只应返回目录。
func TestListDirsFiltersFiles(t *testing.T) {
	body := `{"state":true,"count":4,"data":[` +
		jsonFiles(3) + `,` + jsonDirs("电影", "音乐") + `]}`
	srv := mockFilesServer(t, map[int]string{0: body})
	c := newFilesClient(t, srv.URL)

	dirs, err := c.ListDirs(context.Background(), "0", 0)
	if err != nil {
		t.Fatalf("列举目录失败：%v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("应只返回 2 个目录，实际 %d 个", len(dirs))
	}
	names := []string{dirs[0].Name, dirs[1].Name}
	if strings.Join(names, ",") != "电影,音乐" {
		t.Fatalf("目录名不符，实际 %v", names)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			t.Fatalf("返回了非目录条目：%s", d.Name)
		}
	}
}

// TestListDirsPaginates 目录被文件挤到后面几页时也必须能找到。
// 这是最容易踩的坑：只翻一页再过滤，目录就会「凭空消失」。
func TestListDirsPaginates(t *testing.T) {
	srv := mockFilesServer(t, map[int]string{
		0:   `{"state":true,"count":102,"data":[` + jsonFiles(100) + `]}`,
		100: `{"state":true,"count":102,"data":[` + jsonDirs("深层目录") + `]}`,
	})
	c := newFilesClient(t, srv.URL)

	dirs, err := c.ListDirs(context.Background(), "0", 0)
	if err != nil {
		t.Fatalf("跨页列举失败：%v", err)
	}
	if len(dirs) != 1 || dirs[0].Name != "深层目录" {
		t.Fatalf("应跨页找到「深层目录」，实际 %+v", dirs)
	}
}

// TestListDirsStopsAtLastPage 末页条目不足时应停止翻页，不能一直请求下去。
func TestListDirsStopsAtLastPage(t *testing.T) {
	srv := mockFilesServer(t, map[int]string{
		0: `{"state":true,"count":1,"data":[` + jsonDirs("唯一目录") + `]}`,
	})
	c := newFilesClient(t, srv.URL)

	dirs, err := c.ListDirs(context.Background(), "0", 0)
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("应返回 1 个目录，实际 %d", len(dirs))
	}
}

// TestListDirsRespectsLimit limit 生效，不会无限收集。
func TestListDirsRespectsLimit(t *testing.T) {
	names := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		names = append(names, fmt.Sprintf("d%d", i))
	}
	srv := mockFilesServer(t, map[int]string{
		0:   `{"state":true,"count":120,"data":[` + jsonDirs(names[:100]...) + `]}`,
		100: `{"state":true,"count":120,"data":[` + jsonDirs(names[100:]...) + `]}`,
	})
	c := newFilesClient(t, srv.URL)

	dirs, err := c.ListDirs(context.Background(), "0", 10)
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(dirs) != 10 {
		t.Fatalf("limit=10 应只返回 10 个，实际 %d", len(dirs))
	}
}
