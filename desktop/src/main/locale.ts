// 安装器与首启屏的语言判定：看系统区域，而不是界面里那个语言开关——
// 用户还没进主界面，那个偏好还不存在。
//
// 口径与安装器一致（electron-builder.yml 的 installerLanguages：en_US 在前、zh_CN 在后）：
// 简体中文系统给中文，其余一律英文。GPU/系统区域拿不到时退回英文，与安装器同一条兜底。
import { app } from 'electron';

export type Lang = 'zh' | 'en';

export function systemLang(): Lang {
  try {
    const loc = app.getLocale() || '';
    return /^zh/i.test(loc) ? 'zh' : 'en';
  } catch {
    return 'en';
  }
}
