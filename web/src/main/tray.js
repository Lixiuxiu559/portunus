const { Tray, Menu, nativeImage } = require('electron');
const path = require('path');

let tray = null;

/**
 * 创建系统托盘/菜单栏常驻图标。
 * - Windows：右键弹菜单，左键点击显示主窗口
 * - macOS：左键点击显示主窗口，右键弹菜单（不 setContextMenu，避免单击既弹菜单又开窗口）
 * @param {object} deps
 * @param {() => void} deps.showMainWindow 显示（必要时重建）主窗口
 * @param {() => void} deps.quit 真正退出应用
 */
function createTray({ showMainWindow, quit }) {
  // 图标随 src/main 进 asar：win 用 16pt 彩色 logo；mac 用 Template 模板图
  // （纯黑 + alpha，系统自动适配明暗菜单栏，与系统图标风格统一；@2x 自动拾取）
  const iconFile = process.platform === 'win32' ? '16x16.png' : 'trayTemplate.png';
  const icon = nativeImage.createFromPath(path.join(__dirname, 'icons', iconFile));
  if (process.platform === 'darwin') {
    icon.setTemplateImage(true);
  }

  tray = new Tray(icon);
  tray.setToolTip('Portunus');

  const menu = Menu.buildFromTemplate([
    { label: '打开 Portunus', click: () => showMainWindow() },
    { type: 'separator' },
    { label: '退出', click: () => quit() },
  ]);

  if (process.platform === 'darwin') {
    tray.on('click', () => showMainWindow());
    tray.on('right-click', () => tray.popUpContextMenu(menu));
  } else {
    tray.setContextMenu(menu);
    tray.on('click', () => showMainWindow());
  }

  return tray;
}

module.exports = { createTray };
