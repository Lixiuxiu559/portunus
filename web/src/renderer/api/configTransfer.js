// 配置迁移只使用窄 IPC，不经过 request.js 的写请求重试。
export class ConfigTransferError extends Error {
  constructor(error = {}) {
    super(error.message || '配置迁移暂时不可用，请重试');
    this.name = 'ConfigTransferError';
    this.code = error.code || 'TRANSFER_UNAVAILABLE';
    this.stage = error.stage || 'input';
    this.retryable = !!error.retryable;
    this.operation_id = error.operation_id;
    this.replacement_preview = error.replacement_preview;
    this.field = error.field;
  }
}

export function createConfigTransferBridge(getApi = () => globalThis.window?.api?.configTransfer) {
  const call = async (method, ...args) => {
    const api = getApi();
    if (typeof api?.[method] !== 'function') throw new ConfigTransferError();
    let response;
    try { response = await api[method](...args); }
    catch { throw new ConfigTransferError({ stage: method === 'commitImport' ? 'commit' : 'input' }); }
    if (response?.ok !== true) throw new ConfigTransferError(response?.error);
    return response.data;
  };
  return Object.fromEntries(['summary', 'exportConfig', 'chooseImportFile', 'previewImport', 'commitImport',
    'operationResult', 'recoverOperation', 'recoveryList', 'acknowledge', 'discard'].map((method) => [method, (...args) => call(method, ...args)]));
}

export const configTransfer = createConfigTransferBridge();
