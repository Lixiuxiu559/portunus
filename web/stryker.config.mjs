import path from 'node:path';
import { configForProfile } from './scripts/mutation-js-lib.mjs';

// 只允许入口脚本创建的临时白名单快照；禁止直接在工作区运行 Stryker。
if (process.env.PORTUNUS_MUTATION_STAGE !== process.cwd()
  || !path.basename(process.cwd()).startsWith('portunus-mutation-js-')) {
  throw new Error('请使用 node scripts/mutation-js.mjs；Stryker 只能在隔离快照中运行');
}

export default configForProfile(process.env.PORTUNUS_MUTATION_PROFILE);
