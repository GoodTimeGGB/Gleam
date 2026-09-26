// 脱敏：**离开本机之前**过一道，把已知的机密片段从要发出去的副本里挖掉。
//
// 为什么白名单之外还要这一道：白名单管得住"我们主动放进来什么"，管不住用户
// 自己在描述里粘的那行报错——那里面常带着 `C:\Users\<名字>\...`，有时直接带着一把 key。
// 用户点的是"提交反馈"，不是"把我的路径发给开发者"，所以这句话得有人在发出去之前说。
package feedback

import (
	"strings"

	"gleam/pkg/types"
)

// RedactedMarker 被替换掉的位置留一个显式记号。
//
// 不是删掉就完事：一行凭空少掉的文字看起来像用户自己没写完，
// 收到的人会照着错的前提去查。留记号，读的人就知道这里挖掉过什么。
const RedactedMarker = "‹已脱敏›"

// Redact 返回一份可以外发的副本，以及被替换的总次数。
//
// secrets 是**本机已知的**机密片段（当前生效的密钥明文、工作区与数据目录的绝对路径）。
// 只做已知片段的整串替换，不做正则式猜测打码：猜出来的脱敏会把用户的正文打花，
// 而他以为我们收到的是原文——那比漏一处更糟，因为它是不可见的破坏。
//
// 传入的 f 不被修改：本地归档保留原文（那是用户自己写的、也是他要复查的），
// 只有送出去的那一份经过处理。两份的差异由次数说明，而不是靠事后回忆。
func Redact(f *types.Feedback, secrets []string) (*types.Feedback, int) {
	if f == nil {
		return nil, 0
	}
	list := make([]string, 0, len(secrets))
	for _, s := range secrets {
		// 空串会让"整串替换"变成"每个字符之间都插一次"，比不替换坏得多。
		if s != "" {
			list = append(list, s)
		}
	}
	n := 0
	scrub := func(s string) string {
		for _, sec := range list {
			if c := strings.Count(s, sec); c > 0 {
				n += c
				s = strings.ReplaceAll(s, sec, RedactedMarker)
			}
		}
		return s
	}
	out := *f
	out.Text = scrub(out.Text)
	// 上下文字段本来就只放白名单值，仍然过一遍：以后加字段的人不必先记得
	// "这个字段会不会带路径"，脱敏这道闸已经在整份副本上生效了。
	out.Context.AppVersion = scrub(out.Context.AppVersion)
	out.Context.GoVersion = scrub(out.Context.GoVersion)
	out.Context.OS = scrub(out.Context.OS)
	out.Context.Model = scrub(out.Context.Model)
	out.Context.LLMHost = scrub(out.Context.LLMHost)
	out.Context.TaskID = scrub(out.Context.TaskID)
	out.Context.FailedTool = scrub(out.Context.FailedTool)
	return &out, n
}
