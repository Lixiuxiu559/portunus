import { spawn } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { setTimeout as delay } from 'node:timers/promises';

function signalOwnedTree(child, signal) {
  if (!child.pid) return;
  try {
    // 每次启动创建专属进程组，worker 和 TAP 孙进程继承此组。
    if (process.platform !== 'win32') process.kill(-child.pid, signal);
    else child.kill(signal);
  } catch (error) { if (error.code !== 'ESRCH') throw error; }
}

function groupExists(child) {
  if (!child.pid) return false;
  try {
    process.kill(process.platform === 'win32' ? child.pid : -child.pid, 0);
    return true;
  } catch (error) { if (error.code === 'ESRCH') return false; throw error; }
}

async function stopOwnedTree(child, graceMs) {
  if (!groupExists(child)) return;
  if (process.platform === 'win32') {
    // Windows 没有 POSIX 进程组信号；只终止本次 PID 所属树。
    await new Promise((resolve, reject) => {
      const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
      killer.on('error', reject);
      killer.on('close', resolve);
    });
    return;
  }
  signalOwnedTree(child, 'SIGTERM');
  const deadline = Date.now() + graceMs;
  while (groupExists(child) && Date.now() < deadline) await delay(20);
  signalOwnedTree(child, 'SIGKILL');
}

export async function runNode(args, cwd, env, logFile, { signal, forwardOutput = true, stopGraceMs = 1000, onOutput } = {}) {
  if (signal?.aborted) throw signal.reason || new Error('运行被取消');
  const log = createWriteStream(logFile, { flags: 'wx' });
  const child = spawn(process.execPath, args, { cwd, env, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'] });
  let stopped;
  const stop = () => stopped ||= stopOwnedTree(child, stopGraceMs);
  const abort = () => { void stop().catch(() => {}); };
  const exited = new Promise((resolve, reject) => {
    child.on('error', reject);
    child.on('close', (code, exitSignal) => resolve({ code, signal: exitSignal }));
  });
  let logError;
  log.on('error', (error) => { logError = error; abort(); });
  const output = (stream) => (bytes) => {
    if (forwardOutput) stream.write(bytes);
    if (!logError) log.write(bytes);
    onOutput?.(bytes);
  };
  child.stdout.on('data', output(process.stdout));
  child.stderr.on('data', output(process.stderr));
  signal?.addEventListener('abort', abort, { once: true });
  try {
    if (signal?.aborted) abort();
    const result = await exited;
    if (logError) throw logError;
    if (signal?.aborted) throw signal.reason || new Error('运行被取消');
    return result;
  } finally {
    signal?.removeEventListener('abort', abort);
    await stop();
    await exited.catch(() => {});
    if (!log.destroyed) await new Promise((resolve, reject) => { log.once('error', reject); log.end(resolve); });
  }
}
