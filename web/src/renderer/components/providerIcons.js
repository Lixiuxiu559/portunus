import openai from '../assets/provider-icons/openai.svg?raw';
import anthropic from '../assets/provider-icons/anthropic.svg?raw';
import gemini from '../assets/provider-icons/gemini.svg?raw';
import deepseek from '../assets/provider-icons/deepseek.svg?raw';
import openrouter from '../assets/provider-icons/openrouter.svg?raw';
import siliconflow from '../assets/provider-icons/siliconflow.svg?raw';
import { resolveIcon } from '../utils/channelIcon.js';

export const providerIcons = [
  { id: 'openai', name: 'OpenAI', keywords: 'chatgpt codex', svg: openai },
  { id: 'anthropic', name: 'Anthropic', keywords: 'claude', svg: anthropic },
  { id: 'gemini', name: 'Gemini', keywords: 'google 谷歌', svg: gemini },
  { id: 'deepseek', name: 'DeepSeek', keywords: '深度求索', svg: deepseek },
  { id: 'openrouter', name: 'OpenRouter', keywords: '聚合', svg: openrouter },
  { id: 'siliconflow', name: 'SiliconFlow', keywords: '硅基流动', svg: siliconflow },
];

export function resolveChannelIcon(channel) {
  return resolveIcon(channel, providerIcons.map((entry) => entry.id));
}
