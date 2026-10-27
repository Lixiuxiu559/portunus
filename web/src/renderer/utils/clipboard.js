export const COPY_FEEDBACK_MS = 1600;

// 只有浏览器确认写入成功后才允许显示成功，旧式回退也必须检查返回值。
export async function writeClipboardText(text, { clipboard = globalThis.navigator?.clipboard, document = globalThis.document } = {}) {
  const value = String(text ?? '');
  let clipboardError;
  if (clipboard?.writeText) {
    try {
      await clipboard.writeText(value);
      return;
    } catch (error) {
      clipboardError = error;
    }
  }
  if (!document?.createElement || !document?.execCommand) throw clipboardError || new Error('剪贴板不可用');
  const input = document.createElement('textarea');
  const focused = document.activeElement;
  input.value = value;
  input.setAttribute('readonly', '');
  input.style.position = 'fixed';
  input.style.opacity = '0';
  document.body.appendChild(input);
  try {
    input.select();
    if (!document.execCommand('copy')) throw clipboardError || new Error('剪贴板写入失败');
  } finally {
    input.remove();
    focused?.focus?.({ preventScroll: true });
  }
}

export async function copyWithFeedback(text, { notify, ...environment } = {}) {
  try {
    await writeClipboardText(text, environment);
    notify?.success('已复制');
    return true;
  } catch {
    notify?.danger('复制失败，请手动选择并复制');
    return false;
  }
}
