// Main 与 preload 共用的窄 IPC 白名单，不含文件或 HTTP 能力。
module.exports = Object.freeze({
  summary: 'config-transfer:summary', exportConfig: 'config-transfer:export',
  chooseImportFile: 'config-transfer:choose-import', previewImport: 'config-transfer:preview',
  commitImport: 'config-transfer:commit', operationResult: 'config-transfer:operation-result',
  recoverOperation: 'config-transfer:recover', recoveryList: 'config-transfer:recovery-list',
  acknowledge: 'config-transfer:acknowledge', discard: 'config-transfer:discard',
});
