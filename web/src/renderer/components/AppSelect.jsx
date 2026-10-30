import { Label, ListBox, Select } from '@heroui/react';
import { choiceKey, selectedChoice, selectOptions } from './appChoiceModel';
import './app-choice.css';

export default function AppSelect({ label, value, options, onChange, disabled = false, required = false, placeholder = '请选择', className = '', hideLabel = false }) {
  const items = selectOptions(options, value);
  const selected = required && !String(value ?? '') ? undefined : items.find((option) => option.id === String(value ?? ''));
  return <Select className={`app-choice ${className}`} aria-label={label} selectedKey={selected ? choiceKey(selected.id) : null}
    isDisabled={disabled} isRequired={required} placeholder={placeholder} onSelectionChange={(key) => {
      const option = selectedChoice(items, key);
      if (option) onChange(option.id);
    }}>
    <Label className={`app-choice-label${hideLabel ? ' sr-only' : ''}`}>{label}</Label>
    <Select.Trigger className="app-choice-trigger"><Select.Value className="app-choice-value" /><Select.Indicator /></Select.Trigger>
    <Select.Popover className="app-choice-popover"><ListBox className="app-choice-list" aria-label={label} renderEmptyState={() => <span className="app-choice-empty">暂无可选项</span>}>
      {items.map((option) => <ListBox.Item className="app-choice-option" key={choiceKey(option.id)} id={choiceKey(option.id)} textValue={option.name} isDisabled={option.disabled}>
        <span className="app-choice-option-name">{option.name}</span><ListBox.ItemIndicator />
      </ListBox.Item>)}
    </ListBox></Select.Popover>
  </Select>;
}
