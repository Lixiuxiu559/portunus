import { Component, Suspense } from 'react';
import { Button } from '@heroui/react';

// Suspense 处理等待，错误边界处理资源加载失败，保留外层导航和当前列表。
export default class AsyncContent extends Component {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (this.state.failed) return <div role="alert" className="flex flex-col items-center gap-3 p-6 text-sm text-muted">
      <p>界面加载失败，请重新加载页面。未保存的修改将丢失。</p>
      <Button type="button" size="sm" variant="secondary" onPress={() => window.location.reload()}>重新加载页面</Button>
    </div>;
    return <Suspense fallback={this.props.fallback}>{this.props.children}</Suspense>;
  }
}
