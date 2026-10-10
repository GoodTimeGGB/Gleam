/* 托盘桥：把桌面壳的托盘菜单接到界面上（浏览器里没有 window.gleamDesktop.tray，整块不生效）。
 *
 * 两个方向，各只有一份实现：
 *   去：菜单要显示的「最近会话」和菜单语言只有界面这边知道（数据在本机服务里、语言是界面偏好），
 *       所以这里推给壳——会话走界面同一条 /api/conversations（Go 侧已按最近更新倒序），取前若干条。
 *   回：壳里点了菜单项，动作回到这里，调界面**已有的**入口（#convo-new / openConvo / showView），
 *       托盘不另写一份行为。
 *
 * 什么时候重推：侧栏列表一变（新建 / 改名 / 删除 / 换空间）和界面语言一换。用观察者而不是在
 * app.js 里插回调——托盘的事留在托盘这一个文件里。
 */
const TrayLink = (() => {
  const desk = window.gleamDesktop;
  if (!desk || !desk.tray) return { init() {}, push() {} };

  const MAX = 15; // 与 src/main/tray.ts 的 RECENT_TOP + RECENT_MORE 对齐
  let timer = null;

  function lang() {
    return (document.documentElement.getAttribute('lang') || '').startsWith('en') ? 'en' : 'zh';
  }

  async function push() {
    let recents = [];
    try {
      const res = await api('GET', '/api/conversations');
      recents = (res.conversations || [])
        .filter((c) => c && c.id)
        .slice(0, MAX)
        .map((c) => ({ id: String(c.id), title: String(c.title || '') }));
    } catch {
      // 拉不到就推空列表：菜单里那一段显示成灰的，好过留上一批过期标题
      recents = [];
    }
    try {
      await desk.tray.sync({ lang: lang(), recents });
    } catch {
      // 壳没接上（旧版本壳）就算了，界面不受影响
    }
  }

  // 一次重推会连触发好几次（列表重建），所以压一下。
  function soon() {
    clearTimeout(timer);
    timer = setTimeout(push, 300);
  }

  function init() {
    desk.tray.onAction((a) => {
      if (!a || typeof a.action !== 'string') return;
      if (a.action === 'new-chat') {
        const btn = $('#convo-new');
        if (btn) btn.click();
      } else if (a.action === 'settings') {
        showView('settings');
      } else if (a.action === 'open-convo' && a.id) {
        openConvo(String(a.id));
      }
    });

    const list = $('#convo-list');
    if (list) new MutationObserver(soon).observe(list, { childList: true, subtree: true });
    new MutationObserver(soon).observe(document.documentElement, { attributes: true, attributeFilter: ['lang'] });
    push();
  }

  return { init, push };
})();

TrayLink.init();
