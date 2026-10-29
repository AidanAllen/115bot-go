package bot

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/AidanAllen/115bot-go/internal/p115"
)

// constStringValues 解析文件里所有字符串常量，返回 常量名 -> 值。
func constStringValues(t *testing.T, file string) map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}

	out := map[string]string{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			for _, nm := range vs.Names {
				out[nm.Name] = strings.Trim(lit.Value, `"`)
			}
		}
	}
	return out
}

// switchCases 解析源文件中指定函数的第一个 switch，收集全部 case 分支。
//
// case 既可能是字符串字面量（case "help":），也可能是常量标识符（case cbMain:），
// 后者通过 consts 映射还原成实际值。
//
// 之所以用 AST 而不是手工维护一份「已实现命令」清单：
// 手工清单必然会和真正的 switch 不同步，而不同步的表现正是
// 「菜单里点得到、点了没反应」——这类问题只有用户点了才会暴露。
func switchCases(t *testing.T, file, fn string, consts map[string]string) map[string]bool {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}

	out := map[string]bool{}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || decl.Name.Name != fn || decl.Body == nil {
			return true
		}
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			found = true
			for _, stmt := range sw.Body.List {
				for _, expr := range stmt.(*ast.CaseClause).List {
					switch v := expr.(type) {
					case *ast.BasicLit:
						if v.Kind == token.STRING {
							out[strings.Trim(v.Value, `"`)] = true
						}
					case *ast.Ident:
						if val, ok := consts[v.Name]; ok {
							out[val] = true
						} else {
							t.Errorf("%s 的 case %s 不是已知的字符串常量", fn, v.Name)
						}
					}
				}
			}
			return false
		})
		return false
	})
	if !found {
		t.Fatalf("%s 里没找到 switch", fn)
	}
	return out
}

// TestEveryDeclaredCommandIsImplemented 命令表里每一条都必须有对应分支。
func TestEveryDeclaredCommandIsImplemented(t *testing.T) {
	cases := switchCases(t, "router.go", "runCommand", constStringValues(t, "menu.go"))

	for _, c := range commandSpecs {
		if !cases[c.Name] {
			t.Errorf("命令 /%s 已声明（会出现在 Telegram 菜单和 /help 按钮里），"+
				"但 runCommand 没有对应分支，点了不会有反应", c.Name)
		}
	}
}

// TestEveryImplementedCommandIsDeclared 反向：实现了却没登记的命令，用户在「/」菜单里看不到。
// version 是 v 的别名，允许只登记一个。
func TestEveryImplementedCommandIsDeclared(t *testing.T) {
	cases := switchCases(t, "router.go", "runCommand", constStringValues(t, "menu.go"))
	alias := map[string]bool{"version": true, "set": true, "qrlogin": true}

	for name := range cases {
		known := alias[name]
		for _, c := range commandSpecs {
			if c.Name == name {
				known = true
				break
			}
		}
		if !known {
			t.Errorf("runCommand 实现了 /%s，但命令表里没有，用户无法从菜单看到它", name)
		}
	}
}

// TestBotCommandsWellFormed 提交给 Telegram 的命令必须满足其格式限制，否则整批注册失败。
func TestBotCommandsWellFormed(t *testing.T) {
	cmds := botCommands()
	if len(cmds) != len(commandSpecs) {
		t.Fatalf("命令数量不符：表 %d，提交 %d", len(commandSpecs), len(cmds))
	}

	// Telegram 限制：1-32 字符，仅小写字母、数字、下划线
	nameRe := regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	seen := map[string]bool{}
	for _, c := range cmds {
		if !nameRe.MatchString(c.Command) {
			t.Errorf("命令名不合法：%q", c.Command)
		}
		if seen[c.Command] {
			t.Errorf("命令重复：%q", c.Command)
		}
		seen[c.Command] = true
		if n := len([]rune(c.Description)); n < 3 || n > 256 {
			t.Errorf("命令 /%s 的说明长度 %d 超出 3-256 限制", c.Command, n)
		}
	}
}

// TestHelpMenuButtonsMatchCommands /help 的每个按钮都必须能落到一条真实命令。
func TestHelpMenuButtonsMatchCommands(t *testing.T) {
	rows := helpMenu().InlineKeyboard
	if len(rows) == 0 {
		t.Fatal("帮助菜单没有按钮")
	}

	declared := map[string]bool{}
	for _, c := range commandSpecs {
		declared[c.Name] = true
	}

	n := 0
	for _, row := range rows {
		for _, b := range row {
			if b.CallbackData == nil {
				t.Fatalf("按钮 %q 没有回调数据", b.Text)
			}
			// 返回按钮是导航，不是命令，单独校验（见 nav_test.go）
			if _, isBack := strings.CutPrefix(*b.CallbackData, cbBackPrefix); isBack {
				continue
			}
			name, ok := strings.CutPrefix(*b.CallbackData, cmdPrefix)
			if !ok {
				t.Errorf("按钮 %q 的回调 %q 不是命令前缀", b.Text, *b.CallbackData)
				continue
			}
			if !declared[name] {
				t.Errorf("按钮 %q 指向未登记的命令 %q", b.Text, name)
			}
			n++
		}
	}
	if n != len(commandSpecs) {
		t.Errorf("按钮数 %d 与命令数 %d 不一致", n, len(commandSpecs))
	}
}

// TestHelpTextMentionsEveryCommand /help 正文要能看到全部命令名。
func TestHelpTextMentionsEveryCommand(t *testing.T) {
	text := helpText()
	for _, c := range commandSpecs {
		if !strings.Contains(text, "/"+c.Name) {
			t.Errorf("/help 正文缺少命令 /%s", c.Name)
		}
	}
}

// TestAppPickerUsesRealApps 设备按钮必须都是 115 真正支持的设备，否则点了必然失败。
func TestAppPickerUsesRealApps(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, row := range appPickerMenu(all).InlineKeyboard {
			for _, b := range row {
				if b.CallbackData == nil {
					t.Fatalf("设备按钮 %q 没有回调数据", b.Text)
				}
				data := *b.CallbackData
				if data == cbLoginAll {
					continue
				}
				// 返回按钮是导航，不算设备选择
				if _, isBack := strings.CutPrefix(data, cbBackPrefix); isBack {
					continue
				}
				app, ok := strings.CutPrefix(data, cbLoginAppPrefix)
				if !ok {
					t.Errorf("设备按钮 %q 的回调 %q 前缀不对", b.Text, data)
					continue
				}
				if _, ok := p115.QRAvailableApps[app]; !ok {
					t.Errorf("设备按钮 %q 指向 115 不支持的设备 %q", b.Text, app)
				}
				if qrAppShortName[app] == "" {
					t.Errorf("设备 %q 缺少按钮短名", app)
				}
			}
		}
	}
}

// TestChannelPickerCoversEveryChannel 每个转存通道都得有按钮，否则只能靠记标识。
func TestChannelPickerCoversEveryChannel(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range channelPickerMenu("").InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == nil {
				t.Fatalf("通道按钮 %q 没有回调数据", b.Text)
			}
			id, ok := strings.CutPrefix(*b.CallbackData, cbChannelPrefix)
			if !ok {
				continue
			}
			ch, found := p115.LookupUploadChannel(id)
			if !found {
				t.Errorf("通道按钮 %q 指向不存在的通道 %q", b.Text, id)
				continue
			}
			seen[ch.ID] = true
		}
	}
	for _, ch := range p115.UploadChannels() {
		if !seen[ch.ID] {
			t.Errorf("转存通道 %q（%s）没有对应按钮，用户点不到", ch.ID, ch.Name)
		}
	}
}

// TestChannelPickerCallbacksHandled 面板每个按钮都要落到真实分支。
func TestChannelPickerCallbacksHandled(t *testing.T) {
	kv := constStringValues(t, "menu.go")
	handled := switchCases(t, "router.go", "handleCallback", kv)
	handled[kv["cbChannelPrefix"]] = true
	handled[kv["cbUAPrefix"]] = true
	handled[kv["cbLoginAppPrefix"]] = true
	for v := range ifComparedConsts(t, "router.go", "handleCallback", kv) {
		handled[v] = true
	}

	for _, row := range channelPickerMenu("web").InlineKeyboard {
		for _, b := range row {
			data := *b.CallbackData
			// 返回按钮的分支在 nav_test.go 里单独校验
			if strings.HasPrefix(data, cmdPrefix) || strings.HasPrefix(data, cbChannelPrefix) || strings.HasPrefix(data, cbBackPrefix) {
				continue
			}
			if !handled[data] {
				t.Errorf("通道面板按钮 %q 的回调 %q 没有处理分支", b.Text, data)
			}
		}
	}
}

// TestUAPickerCoversEveryProfile 每个预置 UA 档位都得有按钮，否则用户只能靠记标识。
func TestUAPickerCoversEveryProfile(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range uaPickerMenu("").InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == nil {
				t.Fatalf("UA 按钮 %q 没有回调数据", b.Text)
			}
			id, ok := strings.CutPrefix(*b.CallbackData, cbUAPrefix)
			if !ok {
				continue
			}
			p, found := p115.LookupUAProfile(id)
			if !found {
				t.Errorf("UA 按钮 %q 指向不存在的档位 %q", b.Text, id)
				continue
			}
			seen[p.ID] = true
		}
	}
	for _, p := range p115.UAProfiles() {
		if !seen[p.ID] {
			t.Errorf("UA 档位 %q（%s）没有对应按钮，用户点不到", p.ID, p.Name)
		}
	}
}

// TestUAPickerCallbacksHandled 面板上每个按钮都要落到真实分支，不能点了没反应。
func TestUAPickerCallbacksHandled(t *testing.T) {
	kv := constStringValues(t, "menu.go")
	handled := switchCases(t, "router.go", "handleCallback", kv)
	// 档位选择走前缀匹配，不在 switch 里
	handled[kv["cbUAPrefix"]] = true
	handled[kv["cbLoginAppPrefix"]] = true
	for v := range ifComparedConsts(t, "router.go", "handleCallback", kv) {
		handled[v] = true
	}

	for _, row := range uaPickerMenu("web").InlineKeyboard {
		for _, b := range row {
			data := *b.CallbackData
			// 返回按钮的分支在 nav_test.go 里单独校验
			if strings.HasPrefix(data, cmdPrefix) || strings.HasPrefix(data, cbUAPrefix) || strings.HasPrefix(data, cbBackPrefix) {
				continue
			}
			if !handled[data] {
				t.Errorf("UA 面板按钮 %q 的回调 %q 没有处理分支", b.Text, data)
			}
		}
	}
}

// TestRecommendedAppsAreSafe 推荐列表里不能出现手机 App 本体 / 网页端，
// 否则用户点了就会把自己手机上的 115 踢下线。
func TestRecommendedAppsAreSafe(t *testing.T) {
	risky := map[string]bool{}
	for _, a := range qrRiskyApps {
		risky[a] = true
	}
	for _, a := range qrRecommendedApps {
		if risky[a] {
			t.Errorf("设备 %q 同时出现在推荐与风险列表", a)
		}
		if _, ok := p115.QRAvailableApps[a]; !ok {
			t.Errorf("推荐设备 %q 不被 115 支持", a)
		}
	}
	// 推荐 + 风险应覆盖全部设备，不能有漏网之鱼
	if len(qrRecommendedApps)+len(qrRiskyApps) != len(p115.QRAvailableApps) {
		t.Errorf("设备分组未覆盖全部类型：推荐 %d + 风险 %d ≠ 全部 %d",
			len(qrRecommendedApps), len(qrRiskyApps), len(p115.QRAvailableApps))
	}
}

// ifComparedConsts 收集函数里 `x == 某常量` 形式的判断。
//
// handleCallback 里有一部分回调（如首次绑定）必须在鉴权之前处理，
// 无法写进主 switch，只能通过这种 if 判断覆盖。
func ifComparedConsts(t *testing.T, file, fn string, consts map[string]string) map[string]bool {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}

	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || decl.Name.Name != fn || decl.Body == nil {
			return true
		}
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok || bin.Op != token.EQL {
				return true
			}
			if id, ok := bin.Y.(*ast.Ident); ok {
				if v, ok := consts[id.Name]; ok {
					out[v] = true
				}
			}
			return true
		})
		return false
	})
	return out
}

// TestMainMenuCallbacksHandled 主菜单每个按钮都要有处理分支，不能点了没反应。
func TestMainMenuCallbacksHandled(t *testing.T) {
	kv := constStringValues(t, "menu.go")
	handled := switchCases(t, "router.go", "handleCallback", kv)
	// 设备选择走前缀匹配，不在 switch 里
	handled[kv["cbLoginAppPrefix"]] = true
	// 免鉴权回调走 authorize 之前的 if 判断
	for v := range ifComparedConsts(t, "router.go", "handleCallback", kv) {
		handled[v] = true
	}

	for _, row := range mainMenu().InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == nil {
				t.Fatalf("主菜单按钮 %q 没有回调数据", b.Text)
			}
			data := *b.CallbackData
			if handled[data] {
				continue
			}
			if strings.HasPrefix(data, cmdPrefix) {
				continue
			}
			t.Errorf("主菜单按钮 %q 的回调 %q 没有处理分支", b.Text, data)
		}
	}
	for _, b := range cancelMenu().InlineKeyboard[0] {
		// 返回按钮走前缀分支，由 nav_test.go 校验
		if strings.HasPrefix(*b.CallbackData, cbBackPrefix) {
			continue
		}
		if !handled[*b.CallbackData] {
			t.Errorf("取消按钮的回调 %q 没有处理分支", *b.CallbackData)
		}
	}
	for _, b := range bindMeMenu().InlineKeyboard[0] {
		if !handled[*b.CallbackData] {
			t.Errorf("绑定按钮的回调 %q 没有处理分支", *b.CallbackData)
		}
	}
}
