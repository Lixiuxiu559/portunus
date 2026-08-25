/**
 * 全局背景装饰层（纯视觉，不影响交互）：
 * - 底部几个大型模糊色块缓慢漂浮
 * - 蒙版层在底部透明露出色块，越往上越收敛成主题背景色，保持内容可读
 * 动画与结构样式见 index.css 中 `bg-decor` 一节。
 */
const BLOBS = [
  { left: '-12vw', bottom: '2rem', width: '44rem', height: '44rem', color: 'oklch(65% 0.22 34)', drift: '22s', delay: '0s' },
  { left: '18vw', bottom: '-2rem', width: '38rem', height: '38rem', color: 'oklch(60% 0.2 290)', drift: '26s', delay: '-8s' },
  { right: '-10vw', bottom: '0rem', width: '40rem', height: '40rem', color: 'oklch(62% 0.22 330)', drift: '20s', delay: '-4s' },
  { left: '52vw', bottom: '4rem', width: '26rem', height: '26rem', color: 'oklch(70% 0.13 190)', drift: '24s', delay: '-12s' },
];

export default function Background() {
  return (
    <div className="bg-decor" aria-hidden="true">
      {BLOBS.map((blob, i) => (
        <div
          key={i}
          className="blob"
          style={{
            left: blob.left,
            right: blob.right,
            bottom: blob.bottom,
            width: blob.width,
            height: blob.height,
            background: `radial-gradient(circle at 50% 50%, ${blob.color}, transparent 70%)`,
            animation: `blob-drift ${blob.drift} ease-in-out infinite`,
            animationDelay: blob.delay,
          }}
        />
      ))}
      <div className="mask" />
    </div>
  );
}