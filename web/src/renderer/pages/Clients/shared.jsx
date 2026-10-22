import { useEffect, useState } from 'react';
import { Button, Typography, toast } from '@heroui/react';
import { Check, Copy, ExternalLink, Info } from 'lucide-react';

export async function copy(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success('已复制');
    return true;
  } catch {
    toast.danger('复制失败，请手动选择并复制');
    return false;
  }
}

export function CopyValue({ label, value, display = value, disabled = false, actions = null, wrap = false }) {
  // 复制成功后就地显示对勾，避免连续复制多个值的时候只能靠 toast 确认
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return undefined;
    const timer = setTimeout(() => setCopied(false), 1600);
    return () => clearTimeout(timer);
  }, [copied]);
  return (
    <div className="flex min-w-0 items-center justify-between gap-2 py-3">
      <div className="min-w-0 flex-1">
        <Typography type="body-xs" className="text-muted mb-1">{label}</Typography>
        <code className={`block text-xs ${wrap ? 'break-all' : 'truncate'}`} title={String(display)}>{display}</code>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {actions}
        <Button isIconOnly size="sm" variant="tertiary" aria-label={copied ? `${label}已复制` : `复制${label}`} isDisabled={disabled} onPress={async () => { if (await copy(value)) setCopied(true); }}>
          {copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
        </Button>
      </div>
    </div>
  );
}

export function CodeSample({ title, code, preview = code, disabled = false }) {
  return (
    <div className="overflow-hidden rounded-xl border border-separator bg-background/60">
      <div className="flex flex-wrap items-center justify-between gap-2 bg-surface-secondary/70 px-4 py-2">
        <span className="text-xs text-muted">{title}</span>
        <Button size="sm" variant="tertiary" aria-label={`复制${title}`} isDisabled={disabled} onPress={() => copy(code)}>
          <Copy className="size-3.5" />复制
        </Button>
      </div>
      <pre className="service-code m-0 overflow-auto p-4 text-xs leading-6 select-text"><code>{preview}</code></pre>
    </div>
  );
}

export function DocLink({ href, children }) {
  return <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-accent hover:underline underline-offset-4" onClick={(e) => {
    if (window.api?.openExternal) {
      e.preventDefault();
      window.api.openExternal(href).catch(() => toast.danger('无法打开链接，请复制地址到浏览器'));
    }
  }}>{children}<ExternalLink className="size-3 shrink-0" /></a>;
}

export function GuideStep({ number, title, children }) {
  return (
    <section id={`guide-step-${number}`} className="service-guide-step scroll-mt-5">
      <h2 className="mb-4 flex items-center gap-3 text-base font-semibold">
        <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-accent/10 text-sm text-accent">{number}</span>
        {title}
      </h2>
      <div className="space-y-4 pl-10 text-sm leading-7">{children}</div>
    </section>
  );
}

export function Note({ children }) {
  return <div className="flex gap-2.5 rounded-lg border border-accent/15 bg-accent/5 p-3 text-sm leading-6"><Info className="mt-1 size-4 shrink-0 text-accent" /><div className="min-w-0">{children}</div></div>;
}

export function Parameters({ rows }) {
  return (
    <div className="overflow-x-auto rounded-lg border border-separator">
      <table className="w-full text-left text-sm">
        <thead className="bg-surface-secondary/70 text-muted"><tr><th className="px-3 py-2 font-medium">字段</th><th className="px-3 py-2 font-medium">说明</th></tr></thead>
        <tbody>{rows.map(([key, description]) => <tr key={key} className="border-t border-separator"><td className="px-3 py-2 align-top"><code className="text-xs break-all">{key}</code></td><td className="px-3 py-2 text-muted">{description}</td></tr>)}</tbody>
      </table>
    </div>
  );
}
