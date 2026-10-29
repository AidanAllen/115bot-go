package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestListOfflineTasksRealShape 用 115 线上真实响应形态验证解析。
//
// 关键点：成功响应里**没有 state 字段**，数据直接平铺在顶层。
// 早期实现把 State 当 bool 用，零值 false 会被误判成失败，
// 表现为「网关正常、有任务，却报 115 拒绝」。
func TestListOfflineTasksRealShape(t *testing.T) {
	body := `{"page":1,"page_count":44,"page_row":30,"page_size":30,"count":1294,"quota":235,"total":3000,"tasks":[
		{"info_hash":"2a660258bbd0766727f4cdfee45c299e","add_time":1792512866,"percentDone":100,
		 "display_percent":100,"size":27753762210,"peers":0,"rateDownload":0,
		 "name":"demo.mp4","last_update":1791137354,"left_time":0,
		 "file_id":"3544130319391130746","pick_code":"akoedt6k6x4ofhhtk",
		 "status":2,"display_status":"finished","status_text":"下载成功",
		 "url":"ed2k://|file|demo.mp4|27753762210|2A660258BBD0766727F4CDFEE45C299E|/"}]}`

	srv := mockServer(t, map[string]string{"/offline/list": body})
	c := newWithEndpoints("UID=1", "UA", 5*time.Second, testEndpoints(srv.URL))

	got, err := c.ListOfflineTasks(context.Background(), 1)
	if err != nil {
		t.Fatalf("不应报错（成功响应没有 state 字段）: %v", err)
	}
	if got.Count != 1294 || got.Total != 3000 || got.Quota != 235 {
		t.Fatalf("元信息解析不符: count=%d total=%d quota=%d", got.Count, got.Total, got.Quota)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("任务数期望 1，实际 %d", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.Name != "demo.mp4" || task.Size != 27753762210 {
		t.Fatalf("任务字段不符: name=%q size=%d", task.Name, task.Size)
	}
	if !task.Done() || task.StatusText != "下载成功" {
		t.Fatalf("状态判断不符: status=%d text=%q", task.Status, task.StatusText)
	}
	if task.Progress() != "已完成" {
		t.Fatalf("进度文案不符: %q", task.Progress())
	}
	if task.PickCode != "akoedt6k6x4ofhhtk" || task.FileID != "3544130319391130746" {
		t.Fatalf("转存关键字段缺失: pick_code=%q file_id=%q", task.PickCode, task.FileID)
	}
}

// TestListOfflineTasksExplicitFailure 只有明确给出 state=false 时才判失败。
func TestListOfflineTasksExplicitFailure(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/offline/list": `{"state":false,"error":"请先登录"}`,
	})
	c := newWithEndpoints("UID=1", "UA", 5*time.Second, testEndpoints(srv.URL))
	if _, err := c.ListOfflineTasks(context.Background(), 1); err == nil {
		t.Fatal("明确 state=false 时应报错")
	}
}

// TestAddOfflineRealFailureShape 用线上真实失败响应验证解析：
// 错误文案在 error_msg、逐条明细在 result（不是 error / data）。
func TestAddOfflineRealFailureShape(t *testing.T) {
	body := `{"state":false,"error_msg":"错误的链接","errno":0,
		"result":[{"state":false,"error_msg":"错误的链接","errno":0,"errtype":"err","errcode":10004}],
		"errcode":10004}`
	srv := mockServer(t, map[string]string{"/offline/add": body})
	c := newWithEndpoints("UID=1", "UA", 5*time.Second, testEndpoints(srv.URL))

	items, err := c.AddOfflineURLs(context.Background(), []string{"bad-link"}, "0")
	if err != nil {
		t.Fatalf("有逐条明细时应正常返回，不应整体报错: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d", len(items))
	}
	if items[0].State {
		t.Fatal("该条应为失败")
	}
	if got := items[0].Message(); got != "错误的链接" {
		t.Fatalf("错误文案期望「错误的链接」，实际 %q", got)
	}
}

// TestAddOfflineTopLevelErrorNoResult 顶层失败且没有明细时，应返回可读错误。
func TestAddOfflineTopLevelErrorNoResult(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/offline/add": `{"state":false,"error_msg":"登录已失效","errno":0}`,
	})
	c := newWithEndpoints("UID=1", "UA", 5*time.Second, testEndpoints(srv.URL))

	_, err := c.AddOfflineURLs(context.Background(), []string{"magnet:?xt=urn:btih:a"}, "1")
	if err == nil {
		t.Fatal("应返回顶层错误")
	}
	if err.Error() == "" || err.Error() == "未知错误" {
		t.Fatalf("错误信息应可读，实际 %q", err.Error())
	}
}

// TestAddOfflineUsesURLArrayForm 锁定提交参数名是 url[i]，而不是 urls。
//
// 这是实测踩到的真坑：用 urls 提交时 115 返回「错误的链接」（errcode 10004），
// 看起来像链接格式问题，实际是参数名不对，排查成本很高。
func TestAddOfflineUsesURLArrayForm(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = w.Write([]byte(`{"state":true,"errno":0,
			"result":[{"state":true,"errno":0,"errtype":"","errcode":0,"info_hash":"h1","url":"link-a"}],
			"errcode":0}`))
	}))
	defer srv.Close()

	c := newWithEndpoints("UID=1", "UA", 5*time.Second, testEndpoints(srv.URL))
	items, err := c.AddOfflineURLs(context.Background(), []string{"link-a", "link-b"}, "123")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if got.Get("url[0]") != "link-a" || got.Get("url[1]") != "link-b" {
		t.Fatalf("参数名应为 url[i]，实际: %v", got)
	}
	if v := got.Get("urls"); v != "" {
		t.Fatalf("不应再发送 urls 字段，实际值为 %q", v)
	}
	if got.Get("wp_path_id") != "123" {
		t.Fatalf("wp_path_id 不符: %q", got.Get("wp_path_id"))
	}
	if len(items) != 1 || !items[0].State || items[0].InfoHash != "h1" {
		t.Fatalf("结果解析不符: %+v", items)
	}
}
