#!/usr/bin/env node
/**
 * CRAP 复杂度指标计算器(多语言可扩展,零依赖 Node)。
 *
 * CRAP(m) = comp^2 * (1 - cov)^3 + comp
 *   comp = 函数/方法圈复杂度
 *   cov  = 函数/方法覆盖率(0~1)
 *
 * 复杂度越高、覆盖率越低,CRAP 越高。阈值 30(业界默认),超过即建议重构。
 * 降 CRAP 两条路:拆分降复杂度 或 补测试提覆盖率。
 *
 * ## 为什么用 Node
 * 用 Claude Code 就一定有 Node,但不一定有 Python/uv。用 Node 写脚本 →
 * 任何项目放进来 `node crap.js` 即可,零安装零依赖,利于打包成插件分发。
 *
 * ## 多语言支持现状
 * - java  : ✅ 已实装。解析 JaCoCo jacoco.xml(机器生成、格式固定,手写扫描即可)
 * - python: ⬜ 占位。需 `coverage json`(函数级覆盖) + `radon cc -j`(圈复杂度),按函数名 join
 * - js/ts : ⬜ 占位。需 istanbul coverage-final.json + ESLint complexity/escomplex,按位置 join
 *   注:未来 python/js 的数据源都是 JSON,Node 原生 JSON.parse 直接吃,比 XML 更省事。
 *
 * 新增语言 = 写一个 parse<Lang>() 返回 Method[],注册进 PARSERS。公式/报告层复用。
 *
 * ## 用法
 *   node crap.js [报告路径] [--lang java] [--threshold 30] [--top 30]
 *   不传 --lang 时按报告文件名自动探测。
 * 被 cleaner skill 调用; 只报告不改码, 超阈值以退出码 1 返回。
 */
"use strict";
const fs = require("fs");
const path = require("path");

// Java 项目默认报告路径;其他语言无默认,须显式传路径
const DEFAULT_JAVA_XML =
  "projects/backend/kuavodatahubserver/target/site/jacoco/jacoco.xml";

/** 一个函数/方法的 CRAP 计算单元(语言无关)。 */
class Method {
  constructor(comp, cov, location) {
    this.comp = comp; // 圈复杂度
    this.cov = cov; // 覆盖率 0~1
    this.location = location; // 可读定位
  }
  get crap() {
    return this.comp ** 2 * (1 - this.cov) ** 3 + this.comp;
  }
}

/**
 * 解析 JaCoCo jacoco.xml。
 * JaCoCo 输出是机器生成的确定性格式:<class name="pkg/Cls"> 内含多个
 * <method name="m" ...> ... <counter type="COMPLEXITY" missed covered/>
 * <counter type="LINE" .../> </method>。字段固定,手写扫描可靠。
 * method 级无 BRANCH counter,按业界惯例用 LINE 覆盖率近似。
 */
function parseJava(file) {
  const xml = fs.readFileSync(file, "utf8");
  const methods = [];
  // 逐个 package 块处理,拿到包名上下文
  const pkgRe = /<package\s+name="([^"]+)"[^>]*>([\s\S]*?)<\/package>/g;
  let pm;
  while ((pm = pkgRe.exec(xml)) !== null) {
    const pkgName = pm[1].replace(/\//g, ".");
    const pkgBody = pm[2];
    parseJavaClasses(pkgBody, pkgName, methods);
  }
  return methods;
}

/** 解析一个 package 块内的所有 class/method,追加到 methods。 */
function parseJavaClasses(pkgBody, pkgName, methods) {
  const classRe = /<class\s+name="([^"]+)"[^>]*>([\s\S]*?)<\/class>/g;
  let cm;
  while ((cm = classRe.exec(pkgBody)) !== null) {
    const clsName = cm[1].split("/").pop();
    const clsBody = cm[2];
    // class 块内的每个 method
    const methodRe = /<method\s+name="([^"]+)"[^>]*>([\s\S]*?)<\/method>/g;
    let mm;
    while ((mm = methodRe.exec(clsBody)) !== null) {
      const mName = decodeXml(mm[1]);
      const body = mm[2];
      const comp = counterTotal(body, "COMPLEXITY");
      if (comp === 0) continue;
      const [lineMissed, lineCovered] = counter(body, "LINE");
      const lineTotal = lineMissed + lineCovered;
      const cov = lineTotal ? lineCovered / lineTotal : 0;
      methods.push(new Method(comp, cov, `${pkgName}.${clsName}#${mName}`));
    }
  }
}

/** 从 method 体里读某类型 counter,返回 [missed, covered];无则 [0,0]。 */
function counter(body, type) {
  const re = new RegExp(
    `<counter\\s+type="${type}"\\s+missed="(\\d+)"\\s+covered="(\\d+)"`,
  );
  const m = re.exec(body);
  return m ? [parseInt(m[1], 10), parseInt(m[2], 10)] : [0, 0];
}

function counterTotal(body, type) {
  const [missed, covered] = counter(body, type);
  return missed + covered;
}

/** JaCoCo 里方法名可能含 &lt;init&gt; 等实体,还原常见几个。 */
function decodeXml(s) {
  return s
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&");
}

function parsePython() {
  throw new Error(
    "Python 尚未实装。需先具备两个数据源:\n" +
      "  1. 覆盖率: 装 pytest-cov,跑 `coverage json` 产出函数级覆盖(coverage.xml 无函数级数据)\n" +
      "  2. 复杂度: 装 radon,`radon cc -j` 产出函数级圈复杂度\n" +
      "  然后按函数名 join。两份都是 JSON,Node 原生 JSON.parse 可读。",
  );
}

function parseJs() {
  throw new Error(
    "JS/TS 尚未实装。需先具备两个数据源:\n" +
      "  1. 覆盖率: Jest/Vitest 产出 istanbul coverage-final.json(含函数级 f/fnMap)\n" +
      "  2. 复杂度: istanbul 不带圈复杂度,需 ESLint complexity 规则或 escomplex 另算\n" +
      "  然后按位置 join。两份都是 JSON,Node 原生 JSON.parse 可读。",
  );
}

// parser 注册表:语言 → 解析函数。新增语言在此登记即可。
const PARSERS = {
  java: parseJava,
  python: parsePython,
  js: parseJs,
};

/** 按报告文件名探测语言;探测不出返回空串。 */
function detectLang(file) {
  const base = path.basename(file).toLowerCase();
  if (base.endsWith("jacoco.xml")) return "java";
  if (base === "coverage-final.json" || base.endsWith("lcov.info")) return "js";
  if (base.startsWith("coverage") && base.endsWith(".json")) return "python";
  return "";
}

/** 打印 CRAP 报告,返回退出码(有超阈值项则 1)。 */
function report(methods, threshold, top) {
  methods.sort((a, b) => b.crap - a.crap);
  const over = methods.filter((m) => m.crap > threshold);

  console.log(
    `方法总数: ${methods.length}  |  CRAP>${threshold} 的方法: ${over.length}`,
  );
  console.log("-".repeat(96));
  console.log(
    `${"CRAP".padStart(8)}  ${"复杂度".padStart(5)}  ${"覆盖率".padStart(6)}  方法`,
  );
  console.log("-".repeat(96));
  for (const m of methods.slice(0, top)) {
    const flag = m.crap > threshold ? "  <<<" : "";
    const crap = m.crap.toFixed(1).padStart(8);
    const comp = String(m.comp).padStart(5);
    const cov = (m.cov * 100).toFixed(1).padStart(5);
    console.log(`${crap}  ${comp}  ${cov}%  ${m.location}${flag}`);
  }
  return over.length ? 1 : 0;
}

function parseArgs(argv) {
  const opts = { report: null, lang: null, threshold: 30, top: 30 };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--lang") opts.lang = argv[++i];
    else if (a === "--threshold") opts.threshold = parseFloat(argv[++i]);
    else if (a === "--top") opts.top = parseInt(argv[++i], 10);
    else if (!a.startsWith("--") && opts.report === null) opts.report = a;
  }
  return opts;
}

function main() {
  const opts = parseArgs(process.argv.slice(2));
  const file = opts.report || DEFAULT_JAVA_XML;
  const lang = opts.lang || detectLang(file) || "java"; // 兜底 java,向后兼容

  const parser = PARSERS[lang];
  if (!parser) {
    console.error(`未知语言: ${lang};可选: ${Object.keys(PARSERS).join(", ")}`);
    process.exit(2);
  }
  let methods;
  try {
    methods = parser(file);
  } catch (e) {
    if (e.code === "ENOENT") {
      console.error(`找不到报告 ${file};请先生成对应语言的覆盖率报告`);
      process.exit(2);
    }
    console.error(`[${lang}] ${e.message}`);
    process.exit(3);
  }
  process.exit(report(methods, opts.threshold, opts.top));
}

main();
