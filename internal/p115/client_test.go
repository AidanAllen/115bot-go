package p115

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockServer 构造一个按路径返回预设 JSON 的测试服务。
func mockServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		key := r.URL.Path
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"state":false,"error":"not found"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testEndpoints(base string) endpoints {
	return endpoints{
		Nav:         base + "/nav",
		Category:    base + "/category?cid=%s",
		Files:       base + "/files",
		OfflineAdd:  base + "/offline/add",
		OfflineList: base + "/offline/list?page=%d",
		OfflineDel:  base + "/offline/del",
	}
}

func newTestClient(t *testing.T, base string) *Client {
	t.Helper()
	return newWithEndpoints("USERSESSIONID=test; UID=1", "test-ua", 5*time.Second, testEndpoints(base))
}

func TestCheckLoginSuccess(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/nav": `{"state":true,"error":"","data":{"user_id":88888,"user_name":"测试账号","vip":1}}`,
	})
	c := newTestClient(t, srv.URL)

	info, err := c.CheckLogin(context.Background())
	if err != nil {
		t.Fatalf("期望成功，实际报错：%v", err)
	}
	if info.UserID != 88888 {
		t.Fatalf("user_id 期望 88888，实际 %d", info.UserID)
	}
	if info.UserName != "测试账号" {
		t.Fatalf("user_name 期望「测试账号」，实际 %q", info.UserName)
	}
}

func TestCheckLoginRequired(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/nav": `{"state":false,"data":[],"error":"请先登录,后操作！"}`,
	})
	c := newTestClient(t, srv.URL)

	_, err := c.CheckLogin(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("期望 ErrLoginRequired，实际 %v", err)
	}
}

func TestCheckLoginOtherError(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/nav": `{"state":false,"error":"系统繁忙"}`,
	})
	c := newTestClient(t, srv.URL)

	_, err := c.CheckLogin(context.Background())
	if err == nil || errors.Is(err, ErrLoginRequired) {
		t.Fatalf("期望普通 API 错误，实际 %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *APIError，实际 %T", err)
	}
	if apiErr.Message != "系统繁忙" {
		t.Fatalf("错误文案不符：%q", apiErr.Message)
	}
}

// 关键：cookie 为空时必须本地拦截，不应发出请求。
func TestClientNotReadyBlocksRequest(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/nav": `{"state":true,"data":{"user_id":1}}`,
	})
	c := newWithEndpoints("", "", 5*time.Second, testEndpoints(srv.URL))
	if c.Ready() {
		t.Fatal("cookie/ua 为空时 Ready() 应为 false")
	}
	if _, err := c.CheckLogin(context.Background()); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("期望 ErrLoginRequired，实际 %v", err)
	}
}

func TestAddOfflineURLsArrayResponse(t *testing.T) {
	var gotForm map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = map[string]string{
			"url0":       r.PostFormValue("url[0]"),
			"url1":       r.PostFormValue("url[1]"),
			"wp_path_id": r.PostFormValue("wp_path_id"),
		}
		_, _ = w.Write([]byte(`{"state":true,"error":"","data":[
			{"state":true,"url":"magnet:?xt=urn:btih:aaa","info_hash":"aaa"},
			{"state":false,"url":"magnet:?xt=urn:btih:bbb","error":"任务已存在"}
		]}`))
	}))
	defer srv.Close()

	c := newWithEndpoints("USERSESSIONID=t", "ua", 5*time.Second, testEndpoints(srv.URL))
	items, err := c.AddOfflineURLs(context.Background(),
		[]string{"magnet:?xt=urn:btih:aaa", "magnet:?xt=urn:btih:bbb"}, "123456")
	if err != nil {
		t.Fatalf("期望成功，实际 %v", err)
	}

	if !items[0].State || items[1].State {
		t.Fatalf("期望第 1 条成功、第 2 条失败，实际 %+v", items)
	}
	if gotForm["wp_path_id"] != "123456" {
		t.Fatalf("wp_path_id 未正确传递，实际 %q", gotForm["wp_path_id"])
	}
	if gotForm["url0"] != "magnet:?xt=urn:btih:aaa" || gotForm["url1"] != "magnet:?xt=urn:btih:bbb" {
		t.Fatalf("多条链接应写成 url[i]，实际 url[0]=%q url[1]=%q", gotForm["url0"], gotForm["url1"])
	}
	if len(items) != 2 || items[1].Error != "任务已存在" {
		t.Fatalf("明细解析不符：%+v", items)
	}
}

func TestAddOfflineURLsObjectResponse(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/offline/add": `{"state":true,"data":{"state":true,"url":"ed2k://x","info_hash":"abc"}}`,
	})
	c := newTestClient(t, srv.URL)

	items, err := c.AddOfflineURLs(context.Background(), []string{"ed2k://x"}, "1")
	if err != nil {
		t.Fatalf("期望成功，实际 %v", err)
	}
	if len(items) != 1 || !items[0].State {
		t.Fatalf("单条对象形态解析失败：%+v", items)
	}
}

func TestAddOfflineURLsLoginRequired(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/offline/add": `{"state":false,"error":"请先登录,后操作！"}`,
	})
	c := newTestClient(t, srv.URL)

	_, err := c.AddOfflineURLs(context.Background(), []string{"magnet:?xt=urn:btih:a"}, "1")
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("期望 ErrLoginRequired，实际 %v", err)
	}
}

func TestAddOfflineURLsRequiresCID(t *testing.T) {
	c := New("USERSESSIONID=t", "ua", time.Second)
	if _, err := c.AddOfflineURLs(context.Background(), []string{"magnet:?xt=urn:btih:a"}, ""); err == nil {
		t.Fatal("cid 为空时应本地拦截")
	}
}

// TestCategoryGet 用线上真实结构验证：字段平铺在顶层、目录名是 file_name、
// paths 只含父级链不含自身。
func TestCategoryGet(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/category": `{"state":true,"error":"","errNo":0,"count":"902","size":"773.31GB",
			"folder_count":"88","file_name":"云下载","pick_code":"abc","is_private":0,
			"paths":[{"file_id":0,"file_name":"根目录"}]}`,
	})
	c := newTestClient(t, srv.URL)

	cat, err := c.CategoryGet(context.Background(), "2491055587586025989")
	if err != nil {
		t.Fatalf("期望成功，实际 %v", err)
	}
	if cat.FileName != "云下载" {
		t.Fatalf("目录名解析不符，实际 %q", cat.FileName)
	}
	// paths 不含自身，拼接时应补上
	if got := cat.CategoryPath(); got != "根目录/云下载" {
		t.Fatalf("路径拼接不符，实际 %q", got)
	}
}

// TestCategoryGetEmptyDir 空目录的 count / folder_count 是**数字**而不是字符串
// （非空目录才是字符串）。曾经按 string 声明，导致所有空目录反序列化失败，
// 表现为「刚建好的目录查不到」。这里把两种形态都钉住。
func TestCategoryGetEmptyDir(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/category": `{"state":true,"error":"","errNo":0,"count":0,"size":"0B",
			"folder_count":0,"file_name":"离线","pick_code":"fcs3bxpyr7ugtarrhs",
			"is_private":0,
			"paths":[{"file_id":0,"file_name":"根目录"},
			         {"file_id":3550559425418758033,"file_name":"Telegarm"}]}`,
	})
	c := newTestClient(t, srv.URL)

	cat, err := c.CategoryGet(context.Background(), "3550559509531329963")
	if err != nil {
		t.Fatalf("空目录应解析成功，实际报错 %v", err)
	}
	if cat.FileName != "离线" {
		t.Fatalf("目录名解析不符，实际 %q", cat.FileName)
	}
	if cat.Count != "0" || cat.FolderCount != "0" {
		t.Fatalf("数字型 count 应落成字符串 \"0\"，实际 %q / %q", cat.Count, cat.FolderCount)
	}
	if got := cat.CategoryPath(); got != "根目录/Telegarm/离线" {
		t.Fatalf("多级路径拼接不符，实际 %q", got)
	}
}

// TestFlexString 同一字段忽而数字、忽而字符串，都要能承受。
func TestFlexString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"1020"`, "1020"}, // 非空目录
		{`0`, "0"},         // 空目录
		{`88`, "88"},
		{`""`, ""},
		{`null`, ""},
	}
	for _, c := range cases {
		var v flexString
		if err := json.Unmarshal([]byte(c.in), &v); err != nil {
			t.Fatalf("解析 %s 失败：%v", c.in, err)
		}
		if string(v) != c.want {
			t.Fatalf("解析 %s 得到 %q，期望 %q", c.in, v, c.want)
		}
	}
}

// TestCategoryGetRootLocal 根目录不应发起请求（115 对 cid=0 返回参数错误）。
func TestCategoryGetRootLocal(t *testing.T) {
	c := New("UID=1", "ua", time.Second)
	cat, err := c.CategoryGet(context.Background(), "0")
	if err != nil {
		t.Fatalf("根目录应本地直接返回，实际报错 %v", err)
	}
	if cat.FileName != "根目录" {
		t.Fatalf("根目录名不符，实际 %q", cat.FileName)
	}
}

// TestCategoryGetNotExist 不存在的 cid 应返回可读错误。
func TestCategoryGetNotExist(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/category": `{"state":false,"error":"文件不存在或已删除","errNo":70005}`,
	})
	c := newTestClient(t, srv.URL)

	_, err := c.CategoryGet(context.Background(), "999999999999")
	if err == nil {
		t.Fatal("不存在的 cid 应报错")
	}
	if !strings.Contains(err.Error(), "文件不存在") {
		t.Fatalf("错误信息应包含 115 的原因，实际 %q", err.Error())
	}
}

func TestListOfflineTasks(t *testing.T) {
	srv := mockServer(t, map[string]string{
		"/offline/list": `{"state":true,"count":1,"page":1,"tasks":[{"name":"a.mkv","size":100,"status":2}]}`,
	})
	c := newTestClient(t, srv.URL)

	list, err := c.ListOfflineTasks(context.Background(), 1)
	if err != nil {
		t.Fatalf("期望成功，实际 %v", err)
	}
	if list.Count != 1 || len(list.Tasks) != 1 || list.Tasks[0].Name != "a.mkv" {
		t.Fatalf("列表解析不符：%+v", list)
	}
}

func TestParseSignCheck(t *testing.T) {
	off, length, err := parseSignCheck("0-131071")
	if err != nil || off != 0 || length != 131072 {
		t.Fatalf("解析 0-131071 失败：off=%d len=%d err=%v", off, length, err)
	}
	for _, bad := range []string{"", "abc", "5-1", "1-2-3"} {
		if _, _, err := parseSignCheck(bad); err == nil {
			t.Fatalf("非法输入应报错：%q", bad)
		}
	}
}

func TestUnavailableUploader(t *testing.T) {
	// 没有登录凭据时不可用
	u := NewUploader(New("", "", time.Second), "")
	if u.Available() {
		t.Fatal("无任何凭据时应不可用")
	}
	_, err := u.Upload(context.Background(), "x", "y", 1, "1")
	if !errors.Is(err, ErrUploaderNotConfigured) {
		t.Fatalf("期望 ErrUploaderNotConfigured，实际 %v", err)
	}
}

// TestUploaderRequiresOnlyLogin 只要有登录会话就应具备转存能力：
// /d 不依赖任何第三方 token，扫码登录后即可使用。
func TestUploaderRequiresOnlyLogin(t *testing.T) {
	c := New("UID=1;CID=2", DefaultUA, time.Second)
	u := NewUploader(c, "")
	if !u.Available() {
		t.Fatal("有登录会话时应可转存")
	}
	if _, ok := u.(*webUploader); !ok {
		t.Fatalf("有登录会话时应走网页通道，实际 %T", u)
	}

	if u2 := NewUploader(New("", "", time.Second), ""); u2.Available() {
		t.Fatal("未登录时不应可转存")
	}
}

// TestFileSHA1Range 115 的二次校验要按指定字节区间算 SHA1，这里直接锁定取值。
func TestFileSHA1Range(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "r.txt")
	if err := os.WriteFile(f, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 区间 [2,5] 即 "2345"
	got, err := fileSHA1Range(f, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := sha1.Sum([]byte("2345"))
	if !strings.EqualFold(got, hex.EncodeToString(want[:])) {
		t.Fatalf("区间 SHA1 不符：got %q want %x", got, want)
	}
	// 越界读取不应报错（115 给的区间可能到文件尾）
	if _, err := fileSHA1Range(f, 8, 100); err != nil {
		t.Fatalf("越过文件尾时应按实际长度处理，实际报错: %v", err)
	}
}

func TestFileSHA1(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(f, []byte("abc"), 0o644)

	got, err := FileSHA1(f)
	if err != nil {
		t.Fatal(err)
	}
	// echo -n abc | sha1sum = a9993e364706816aba3e25717850c26c9cd0d89d
	want := "A9993E364706816ABA3E25717850C26C9CD0D89D"
	if got != want {
		t.Fatalf("SHA1 期望 %s，实际 %s", want, got)
	}
}
