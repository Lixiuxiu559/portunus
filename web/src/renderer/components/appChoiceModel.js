// Prefix every key so an explicitly empty option remains distinct from no selection.
export const choiceKey = (value) => `value:${String(value)}`;
export function selectOptions(options, value) {
  const items = options.map((option) => ({ ...option, id: String(option.id) }));
  const current = String(value ?? '');
  if (current && !items.some((option) => option.id === current)) {
    items.unshift({ id: current, name: `${current}（当前值不可用）`, disabled: true });
  }
  return items;
}
export function selectedChoice(options, key) {
  return key == null ? undefined : options.find((option) => choiceKey(option.id) === key && !option.disabled);
}
export function comboOptions(options) {
  return [...new Set(options.filter((option) => typeof option === 'string' && option.length > 0))];
}
