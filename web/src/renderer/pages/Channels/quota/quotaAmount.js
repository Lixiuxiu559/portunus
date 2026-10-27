// 金额只做字符串排版，不转浮点、不在前端计算余额或汇率。
export function formatQuotaAmount(value) {
  if (value === undefined || value === null) return '未知';
  return String(value).replace(/^(-?)(\d+)(\.\d+)?$/, (_, sign, integer, decimal = '') => `${sign}${integer.replace(/\B(?=(\d{3})+(?!\d))/g, ',')}${decimal}`);
}

// 摘要金额按十进制字符串舍入；不改变快照和详情中的完整值。
export function formatCompactQuotaAmount(value, unit) {
  if (unit?.kind !== 'money' || value == null) return formatQuotaAmount(value);
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(String(value));
  if (!match) return formatQuotaAmount(value);
  const [, sign, integer, fraction = ''] = match;
  const digits = fraction.padEnd(3, '0');
  const cents = BigInt(integer) * 100n + BigInt(digits.slice(0, 2));
  const nonzero = /[1-9]/.test(integer + fraction);
  if (cents === 0n && nonzero) return sign ? '>-0.01' : '<0.01';
  const rounded = cents + (digits[2] >= '5' ? 1n : 0n);
  const result = `${sign && rounded !== 0n ? '-' : ''}${rounded / 100n}.${String(rounded % 100n).padStart(2, '0')}`;
  return formatQuotaAmount(result);
}

