const { contextBridge } = require('electron');

// IPC 桥接占位：模板阶段不暴露任何主进程能力，后续按需在此添加
contextBridge.exposeInMainWorld('api', {});
