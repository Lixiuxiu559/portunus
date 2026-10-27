import { useEffect, useRef, useState } from 'react';
import { Button, toast } from '@heroui/react';
import { Check, Copy } from 'lucide-react';
import { COPY_FEEDBACK_MS, copyWithFeedback } from '../utils/clipboard';

export default function CopyButton({ value, label = '复制', text = label, iconOnly = true, disabled = false, size = 'sm', variant = 'tertiary', className = '' }) {
  const [copied, setCopied] = useState(false);
  const [pending, setPending] = useState(false);
  const timer = useRef(null);
  const generation = useRef(0);
  const copying = useRef(false);
  useEffect(() => {
    generation.current += 1;
    copying.current = false;
    setCopied(false);
    setPending(false);
    return () => {
      generation.current += 1;
      clearTimeout(timer.current);
    };
  }, [value]);
  const copy = async () => {
    if (disabled || copying.current) return;
    const current = generation.current;
    copying.current = true;
    setCopied(false);
    setPending(true);
    clearTimeout(timer.current);
    const success = await copyWithFeedback(value, { notify: toast });
    if (current !== generation.current) return;
    copying.current = false;
    setPending(false);
    setCopied(success);
    if (success) timer.current = setTimeout(() => setCopied(false), COPY_FEEDBACK_MS);
  };
  return <Button type="button" isIconOnly={iconOnly} size={size} variant={variant} className={className}
    aria-label={copied ? `${label}：已复制` : label} title={copied ? '已复制' : label}
    isDisabled={disabled || pending} onPress={copy}>
    {copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
    {!iconOnly && (copied ? '已复制' : text)}
  </Button>;
}
