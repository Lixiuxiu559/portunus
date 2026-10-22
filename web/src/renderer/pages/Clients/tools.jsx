import { BookOpen } from 'lucide-react';
import { ClaudeMark, OpenAIMark } from '../../components/BrandMarks';
import { additionalClientGuides } from '../../utils/clientGuides';

// 接入示例里的客户端指南（Claude Code / Codex 固定，其余来自 clientGuides）。
export const CLIENT_GUIDES = [
  { id: 'claude', label: 'Claude Code', icon: <ClaudeMark className="size-4" />, description: '使用本地网关，为 Claude Code 接入你的模型分组。', command: 'claude', docs: 'https://code.claude.com/docs/zh-CN/setup' },
  { id: 'codex', label: 'Codex', icon: <OpenAIMark className="size-4" />, description: '使用 OpenAI Responses 接口，将 Codex CLI 的请求交给 Portunus 路由。', command: 'codex', docs: 'https://developers.openai.com/codex/cli/' },
  ...additionalClientGuides.map((c) => ({ ...c, icon: <BookOpen className="size-4 shrink-0" /> })),
];

const API_TOOL = { id: 'api', label: 'API', icon: <BookOpen className="size-4" /> };

// 左树的叶子定义；接入示例的 id 与 sectionState.SECTION_TOOLS 保持一致。
export const TREE_SECTIONS = [
  {
    id: 'examples',
    label: '接入示例',
    children: [API_TOOL, ...CLIENT_GUIDES.map((c) => ({ id: c.id, label: c.label, icon: c.icon }))],
  },
  {
    id: 'config',
    label: '客户端配置',
    children: [{ id: 'claude', label: 'Claude Code', icon: <ClaudeMark className="size-4" /> }],
  },
];

export function findClientGuide(id) {
  return CLIENT_GUIDES.find((c) => c.id === id) || CLIENT_GUIDES[0];
}
