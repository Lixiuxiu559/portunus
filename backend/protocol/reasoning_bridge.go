package protocol

import (
	"encoding/base64"
	"strings"
)

// 本文件是「思考内容往返桥」：让 reasoning_content 在 anthropic ↔ openai 的
// 跨协议往返中无损，领域背景见 docs/research/reasoning-content-passback.md。
//
// 问题：DeepSeek 等 thinking 上游要求多轮对话把上一轮 reasoning_content 原样回传，
// 而 Anthropic Messages 协议没有承载该字段的位置——唯一能塞东西的是 thinking 块的
// signature。此前 signature 写死常量 "sig"（见 anthropic.go / anthropic_stream.go），
// 只满足「签名非空」这一形式要求、不含任何信息：客户端一旦不回传 thinking 明文
// （redacted_thinking 只剩密文、上下文压缩、客户端规范化改写），网关就无从还原原始
// reasoning_content，只能回填占位字符串。
//
// 为什么这会让缓存失效：占位回填意味着**同一段历史在不同轮次产出不同字节**。上游
// （DeepSeek 等）的上下文缓存是自动前缀缓存——按 token 前缀精确匹配，前缀里任一
// 字节变化，从该点起整段缓存失效。于是每经过一轮，上一轮的真实思考内容退化成占位，
// 改写点之后的近期上下文全部重算，命中率骤降。
//
// 对照：DSH 这类原生 openai 客户端走同协议直通（convert.go 的 rewriteSameProtocol），
// 历史消息只换 model 字段、其余逐字节原样透传，reasoning_content 稳定，缓存正常命中。
// 差异不在「缓存开关」，而在跨协议重建时思考内容的有损往返。
//
// 解法：把原始 reasoning_content 做 base64 封装进 signature（带版本前缀）。客户端
// 原样回传 signature 时，网关解码还原出**逐字节一致**的 reasoning_content，前缀稳定，
// 缓存可命中；客户端确实丢了内容（上游加密的密文、旧格式签名）时才退回占位。
//
// 编码形态：portunus-reasoning-v1:<base64(原始 reasoning_content)>
//
// 代价：signature 会随请求每轮回传，思考内容因此在上游请求体里出现两次（thinking
// 明文 + signature 载荷），输入 token 相应增加。换来的是前缀逐字节稳定。若将来
// 需要收敛该开销，可在 EncodeReasoningSignature 加长度阈值（超限退化为短标记），
// 但那会重新引入长思考场景下的前缀漂移，需一并评估。

// ReasoningSignaturePrefix 是桥载荷的版本前缀。将来换编码格式时，旧前缀仍可识别
// 并解码（向后兼容）；识别不了的一律当普通签名处理，安全降级为「读 thinking 明文」。
const ReasoningSignaturePrefix = "portunus-reasoning-v1:"

// reasoningSignatureFallback 是无可封装内容时的非空签名占位。部分客户端校验签名
// 非空才肯在下一轮回传 thinking 块，空签名会让整块思考内容丢失——这是 "sig" 常量
// 原本承担的唯一职责，桥载荷为空时保留它，行为与改动前一致。
const reasoningSignatureFallback = "sig"

// EncodeReasoningSignature 把原始 reasoning_content 封装成 thinking 块的 signature。
// reasoning 为空时返回空串（调用方据此走 reasoningSignatureFallback）。
func EncodeReasoningSignature(reasoning string) string {
	if reasoning == "" {
		return ""
	}
	return ReasoningSignaturePrefix + base64.StdEncoding.EncodeToString([]byte(reasoning))
}

// DecodeReasoningSignature 从 signature 还原原始 reasoning_content。
// 不是桥格式（上游原生签名、旧常量 "sig"、任意客户端签名）或 base64 损坏时返回
// ok=false，调用方退回读 thinking 明文。
func DecodeReasoningSignature(sig string) (string, bool) {
	if !strings.HasPrefix(sig, ReasoningSignaturePrefix) {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sig, ReasoningSignaturePrefix))
	if err != nil {
		return "", false
	}
	return string(raw), true
}
