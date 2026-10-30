// uploadtest 用真实账号验证上传通道（init → OSS → 落盘核对）。
//
// 这是给运维排障用的诊断工具，不参与常驻运行。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/AidanAllen/115bot-go/internal/p115"
)

// accountFile 与 qrlogin 输出的账号文件结构一致。
type accountFile struct {
	Cookie    string    `json:"cookie"`
	UserAgent string    `json:"user_agent"`
	UserID    int64     `json:"user_id"`
	UserName  string    `json:"user_name"`
	App       string    `json:"app"`
	SavedAt   time.Time `json:"saved_at"`
}

func main() {
	account := flag.String("account", "/root/.115bot/account.json", "账号文件")
	cid := flag.String("cid", "", "目标目录 cid（必填）")
	file := flag.String("file", "", "待上传文件（留空则生成测试文件）")
	name := flag.String("name", "", "上传后的文件名（留空用源文件名）")
	size := flag.Int64("size", 1<<20, "生成测试文件的大小")
	keep := flag.Bool("keep", false, "保留上传后的文件（默认删除）")
	partSize := flag.Int64("partsize", 0, "分片大小（默认 10MB，调小便于快速验证分片链路）")
	channel := flag.String("channel", "", "上传通道（web/app/alipaymini/wechatmini，留空用默认 web）")
	timeout := flag.Duration("timeout", 10*time.Minute, "总超时")
	flag.Parse()

	if err := run(*account, *cid, *file, *name, *size, *keep, *partSize, *channel, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "失败: %v\n", err)
		os.Exit(1)
	}
}

func run(accountPath, cid, file, name string, size int64, keep bool, partSize int64, channel string, timeout time.Duration) error {
	if cid == "" {
		return fmt.Errorf("必须指定 -cid")
	}

	raw, err := os.ReadFile(accountPath)
	if err != nil {
		return fmt.Errorf("读取账号文件失败: %w", err)
	}
	var acc accountFile
	if err := json.Unmarshal(raw, &acc); err != nil {
		return fmt.Errorf("解析账号文件失败: %w", err)
	}
	if acc.Cookie == "" {
		return fmt.Errorf("账号文件缺少 cookie")
	}
	fmt.Printf("账号: %s (uid=%d) 设备=%s\n", acc.UserName, acc.UserID, acc.App)

	// 准备待上传文件
	local := file
	if local == "" {
		local = filepath.Join(os.TempDir(), fmt.Sprintf("115up_%d.bin", time.Now().Unix()))
		if err := writeTestFile(local, size); err != nil {
			return err
		}
		defer func() { _ = os.Remove(local) }()
	}
	fi, err := os.Stat(local)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}
	if name == "" {
		name = filepath.Base(local)
	}

	sum, err := p115.FileSHA1(local)
	if err != nil {
		return err
	}
	fmt.Printf("文件: %s  大小=%s  sha1=%s\n", name, humanSize(fi.Size()), sum)

	client := p115.New(acc.Cookie, acc.UserAgent, 60*time.Second)
	up := p115.NewUploader(client, channel)
	if !up.Available() {
		return fmt.Errorf("上传器不可用")
	}
	if partSize > 0 {
		p115.SetUploadPartSize(up, partSize)
	}
	fmt.Printf("通道: %s\n", up.Name())
	if partSize > 0 {
		fmt.Printf("分片大小: %s（仅调试用，>5GB 才会自动分片）\n", humanSize(partSize))
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	pick, err := up.Upload(ctx, local, name, fi.Size(), cid)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	speed := float64(fi.Size()) / elapsed.Seconds() / (1 << 20)
	fmt.Printf("✓ 上传成功  耗时=%.1fs  平均=%.2f MB/s  pick_code=%s\n", elapsed.Seconds(), speed, pick)

	// 落盘核对
	fmt.Println("\n=== 落盘核对 ===")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()

	list, err := client.ListFiles(ctx2, cid, 0, 100)
	if err != nil {
		return fmt.Errorf("列目录失败: %w", err)
	}
	var found *p115.FileEntry
	for i := range list.Data {
		if list.Data[i].Name == name {
			found = &list.Data[i]
			break
		}
	}
	if found == nil {
		fmt.Printf("✗ 前 %d 项中未找到 %s（可能排序延迟）\n", len(list.Data), name)
		return fmt.Errorf("落盘核对失败")
	}
	fmt.Printf("✓ 已入库: name=%s size=%d sha1=%s pick=%s fid=%s\n",
		found.Name, found.Size, found.Sha1, found.PickCode, found.FID)
	if found.PickCode != pick {
		fmt.Printf("⚠ pick_code 不一致：上传返回 %s，列表显示 %s\n", pick, found.PickCode)
	}
	if found.Sha1 != sum {
		fmt.Printf("⚠ sha1 不一致：本地 %s，云端 %s\n", sum, found.Sha1)
	}

	if !keep {
		if err := client.DeleteFiles(ctx2, []string{found.FID}); err != nil {
			return fmt.Errorf("删除测试文件失败: %w", err)
		}
		fmt.Println("✓ 已删除测试文件")
	}
	return nil
}

func writeTestFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	const block = 64 << 10
	buf := make([]byte, block)
	for i := range buf {
		buf[i] = byte(i * 31 % 251)
	}
	var written int64
	for written < size {
		n := int64(block)
		if remain := size - written; remain < n {
			n = remain
		}
		if _, err := f.Write(buf[:n]); err != nil {
			return err
		}
		written += n
	}
	return nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
