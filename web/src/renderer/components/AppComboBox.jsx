import { ComboBox, Input, Label, ListBox } from '@heroui/react';
import { choiceKey, comboOptions } from './appChoiceModel';
import './app-choice.css';

export default function AppComboBox({ label, value, onChange, options, placeholder, disabled = false, className = '' }) {
  const items = comboOptions(options);
  return <ComboBox className={`app-choice ${className}`} aria-label={label} allowsCustomValue allowsEmptyCollection
    inputValue={value} selectedKey={items.includes(value) ? choiceKey(value) : null} isDisabled={disabled}
    onInputChange={onChange} onSelectionChange={(key) => {
      // Typing custom text clears the selection internally; that must never clear the text.
      const option = items.find((item) => choiceKey(item) === key);
      if (option !== undefined) onChange(option);
    }}>
    <Label className="app-choice-label">{label}</Label>
    <ComboBox.InputGroup className="app-choice-trigger app-choice-input-group">
      <Input className="app-choice-input" placeholder={placeholder} />
      <ComboBox.Trigger aria-label={`${label}候选项`} />
    </ComboBox.InputGroup>
    <ComboBox.Popover className="app-choice-popover"><ListBox className="app-choice-list" aria-label={label} renderEmptyState={() => <span className="app-choice-empty">无匹配项，可继续输入自定义模型名</span>}>
      {items.map((name) => <ListBox.Item className="app-choice-option" key={choiceKey(name)} id={choiceKey(name)} textValue={name}><span className="app-choice-option-name">{name}</span><ListBox.ItemIndicator /></ListBox.Item>)}
    </ListBox></ComboBox.Popover>
  </ComboBox>;
}
