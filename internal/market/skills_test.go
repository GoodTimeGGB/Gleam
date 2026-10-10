package market

import (
	"regexp"
	"strings"
	"testing"
)

// skillToolAllowlist 技能步骤里允许出现的工具名。
//
// 这张表是**判据的一部分**，不是从代码里推出来的：技能是随程序发布的内容，
// 一旦某条写错工具名（`file_read` 而不是 `file.read`），装到用户机器上才会在执行时炸——
// 而那时错误信息离"技能模板写错了"很远。所以在这里挡一道。
//
// 加了新工具要同步这张表；忘了改的后果是新增技能被判红，而不是静默放行。
var skillToolAllowlist = map[string]bool{
	"file.list": true, "file.read": true, "file.write": true, "file.mkdir": true,
	"file.move": true, "file.delete": true, "file.search": true,
	"memory.save": true, "memory.search": true, "memory.delete": true,
	"web.fetch": true, "reply": true, "prompt.run": true, "shell.exec": true,
	"git.branch": true, "git.commit": true, "git.push": true,
}

var kebabRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var paramRefRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// TestSkillCatalogIntegrity 整个技能目录的机械判据。
//
// 逐条抓的是"装到用户机器上才会暴露"的那类错：名字重复、工具名打错、
// 引用了不存在的步骤或没声明的参数。这些都**不会**在编译期被发现。
func TestSkillCatalogIntegrity(t *testing.T) {
	if len(SkillCatalog) < 20 {
		t.Fatalf("技能目录只有 %d 条：这个判据的前提是目录够大，先补内容或修判据", len(SkillCatalog))
	}
	cats := map[string]bool{}
	for _, c := range SkillCategories() {
		cats[c] = true
	}

	seenName := map[string]bool{}
	for _, sk := range SkillCatalog {
		if !kebabRe.MatchString(sk.Name) {
			t.Errorf("技能名 %q 不是 kebab-case", sk.Name)
		}
		if seenName[sk.Name] {
			t.Errorf("技能名重复：%q（重名会让 install/run 落到其中一条上，另一条永远装不上）", sk.Name)
		}
		seenName[sk.Name] = true
		if strings.TrimSpace(sk.Description) == "" {
			t.Errorf("%s 没有描述：市场里那一格会空着", sk.Name)
		}
		if sk.Category == "" || !cats[sk.Category] {
			t.Errorf("%s 的分类 %q 不在 SkillCategories() 里：筛选栏会少一格或对不上", sk.Name, sk.Category)
		}

		params := map[string]bool{}
		for _, p := range sk.Params {
			params[p] = true
		}
		steps := map[string]bool{}
		for _, st := range sk.Steps {
			if st.ID == "" {
				t.Errorf("%s 有步骤没有 id", sk.Name)
			}
			if steps[st.ID] {
				t.Errorf("%s 步骤 id 重复：%q", sk.Name, st.ID)
			}
			steps[st.ID] = true
		}
		for _, st := range sk.Steps {
			if !skillToolAllowlist[st.Tool] {
				t.Errorf("%s 的步骤 %s 用了未知工具 %q（装到用户机器上才会炸）", sk.Name, st.ID, st.Tool)
			}
			for _, dep := range st.DependsOn {
				if !steps[dep] {
					t.Errorf("%s 的步骤 %s 依赖了不存在的步骤 %q", sk.Name, st.ID, dep)
				}
			}
			for _, ref := range refsIn(st.Args) {
				// 带点的引用是「步骤 id + 字段路径」，不带点的是「声明过的参数」
				if i := strings.IndexByte(ref, '.'); i > 0 {
					if !steps[ref[:i]] {
						t.Errorf("%s 的步骤 %s 引用了不存在的步骤 %q", sk.Name, st.ID, ref[:i])
					}
					continue
				}
				if !params[ref] {
					t.Errorf("%s 的步骤 %s 引用了没声明的参数 %q（安装时没人填它，值会是空的）", sk.Name, st.ID, ref)
				}
			}
		}
	}
}

// refsIn 取出参数值里出现的所有 {{...}} 引用（含嵌套 map/slice）。
func refsIn(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		for _, m := range paramRefRe.FindAllStringSubmatch(x, -1) {
			out = append(out, strings.TrimSpace(m[1]))
		}
	case map[string]any:
		for _, vv := range x {
			out = append(out, refsIn(vv)...)
		}
	case []any:
		for _, vv := range x {
			out = append(out, refsIn(vv)...)
		}
	}
	return out
}

// TestSkillCategoriesAreDistinctAndOrdered 分类表本身要干净：不重复、非空、顺序稳定。
func TestSkillCategoriesAreDistinctAndOrdered(t *testing.T) {
	cats := SkillCategories()
	if len(cats) < 6 {
		t.Fatalf("分类太少（%d）：分不出类等于没分", len(cats))
	}
	seen := map[string]bool{}
	for i, c := range cats {
		if strings.TrimSpace(c) == "" {
			t.Errorf("第 %d 个分类是空的", i)
		}
		if seen[c] {
			t.Errorf("分类重复：%q", c)
		}
		seen[c] = true
	}
	// 每个分类至少要有一条技能，否则界面上会出现点了没结果的空白格
	used := map[string]bool{}
	for _, sk := range SkillCatalog {
		used[sk.Category] = true
	}
	for _, c := range cats {
		if !used[c] {
			t.Errorf("分类 %q 下一条技能都没有：筛选栏里那一格点了是空的", c)
		}
	}
}
