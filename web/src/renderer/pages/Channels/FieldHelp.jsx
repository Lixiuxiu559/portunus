import { Button, Popover } from '@heroui/react';
import { CircleHelp } from 'lucide-react';
import './channel-form.css';

/** 帮助按需打开，保留键盘可达性，不触发表单提交。 */
export default function FieldHelp({ title, children }) {
  return <Popover>
    <Button type="button" isIconOnly size="sm" variant="tertiary" className="channel-field-help" aria-label={`了解${title}`}><CircleHelp className="size-3.5" /></Button>
    <Popover.Content placement="bottom end" className="channel-help-popover"><Popover.Dialog aria-label={`${title}说明`} className="channel-help-dialog"><Popover.Heading>{title}</Popover.Heading><div>{children}</div></Popover.Dialog></Popover.Content>
  </Popover>;
}
