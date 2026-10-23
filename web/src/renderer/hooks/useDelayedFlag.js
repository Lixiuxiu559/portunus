import { useEffect, useState } from 'react';

// 只延迟显示反馈，不延迟请求或真实内容。
export default function useDelayedFlag(active, delay = 200) {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    if (!active) { setVisible(false); return undefined; }
    const timer = setTimeout(() => setVisible(true), delay);
    return () => clearTimeout(timer);
  }, [active, delay]);
  return active && visible;
}
