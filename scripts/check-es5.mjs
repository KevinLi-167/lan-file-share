// 老安卓兼容性校验脚本
//
// 用途：在改动 web/ 下的页面之后跑一次，确保没有混进老 WebKit 不认识的语法和 API。
// 目标环境：Android 4.3 及更老（WebKit 534 内核），相当于 ECMAScript 5 语法子集。
//
// 用法：
//   node scripts/check-es5.mjs
//
// 依赖 acorn（仅用于把内联脚本按 ECMAScript 5 解析）。

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const require = createRequire(import.meta.url);

// strict=true 表示这个页面必须能在老安卓上跑，命中即失败。
// strict=false 表示只在提示层面报告，不阻断（管理页只在 Windows 现代浏览器里打开）。
const TARGETS = [
  { file: 'web/client.html', strict: true, allow: [] },
  // 探测页的职责就是「检测这些能力存不存在」，所以出现这些词是正常的
  { file: 'web/probe.html', strict: true, allow: ['EventSource', 'new Blob(', 'createObjectURL', 'new Map(', 'new Set(', 'Symbol('] },
  { file: 'web/admin.html', strict: false, allow: [] },
];

// 这些 API 在 Android 4.3 的 WebKit 上不存在，写进去就是白屏
const BANNED_APIS = [
  ['fetch(', '老 WebKit 没有 fetch，请用 XMLHttpRequest'],
  ['new Promise', '没有 Promise，请用回调'],
  ['Object.assign', '没有 Object.assign'],
  ['Object.entries', '没有 Object.entries'],
  ['Object.values', '没有 Object.values'],
  ['Object.fromEntries', '没有 Object.fromEntries'],
  ['Array.from', '没有 Array.from'],
  ['Array.of', '没有 Array.of'],
  ['.includes(', '没有 String/Array.prototype.includes'],
  ['.startsWith(', '没有 String.prototype.startsWith'],
  ['.endsWith(', '没有 String.prototype.endsWith'],
  ['.padStart(', '没有 String.prototype.padStart'],
  ['.padEnd(', '没有 String.prototype.padEnd'],
  ['.repeat(', '没有 String.prototype.repeat'],
  ['.closest(', '没有 Element.closest'],
  ['EventSource', '没有 EventSource，本项目改用轮询'],
  ['new Map(', '没有 Map'],
  ['new Set(', '没有 Set'],
  ['new WeakMap(', '没有 WeakMap'],
  ['Symbol(', '没有 Symbol'],
  ['createObjectURL', '没有 URL.createObjectURL'],
  ['new Blob(', '没有 Blob 构造器'],
];

// 老 WebKit 不支持或支持很差的 CSS 特性
const BANNED_CSS = [
  ['var(--', '不支持 CSS 自定义属性'],
  ['display: grid', '不支持 grid 布局'],
  ['display:grid', '不支持 grid 布局'],
  ['backdrop-filter', '不支持 backdrop-filter'],
  ['aspect-ratio', '不支持 aspect-ratio'],
  ['position: sticky', '不支持 sticky 定位'],
  ['position:sticky', '不支持 sticky 定位'],
  ['@supports', '不支持 @supports'],
];

// 把 HTML 注释替换成等量空白，既不影响后续按行号定位，也不会把注释里的
// 「不要用 EventSource」这类说明误判成真实用法。
function blankOutComments(html) {
  return html.replace(/<!--[\s\S]*?-->/g, (block) => block.replace(/[^\n]/g, ' '));
}

function extractScripts(html) {
  const scripts = [];
  const re = /<script\b(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/gi;
  let match;
  while ((match = re.exec(html)) !== null) {
    scripts.push({ code: match[1], offset: match.index });
  }
  return scripts;
}

function lineOf(html, index) {
  return html.slice(0, index).split('\n').length;
}

// acorn 可能装在项目里，也可能装在别处的 node_modules（通过 NODE_PATH 指定），
// 两种位置都试一下，避免为了跑一个检查脚本而强制在项目里装依赖。
function loadAcorn() {
  const candidates = ['acorn'];

  if (process.env.NODE_PATH) {
    for (const dir of process.env.NODE_PATH.split(path.delimiter)) {
      if (dir) {
        candidates.push(path.join(dir, 'acorn'));
      }
    }
  }

  for (const candidate of candidates) {
    try {
      return require(candidate);
    } catch (error) {
      // 换下一个候选位置
    }
  }
  return null;
}

const acorn = loadAcorn();
if (!acorn) {
  console.error('缺少 acorn 依赖，请先执行：');
  console.error('  npm install');
  console.error('或指定已安装位置：');
  console.error('  NODE_PATH=<node_modules 目录> node scripts/check-es5.mjs');
  process.exit(2);
}

let problemCount = 0;

for (const target of TARGETS) {
  const fullPath = path.join(root, target.file);
  if (!fs.existsSync(fullPath)) {
    console.log(`跳过（文件不存在）：${target.file}`);
    continue;
  }

  const raw = fs.readFileSync(fullPath, 'utf8');
  const html = blankOutComments(raw);
  const label = target.strict ? '严格' : '提示';
  const problems = [];

  // 1) 按 ES5 解析每一个内联脚本
  for (const script of extractScripts(html)) {
    try {
      acorn.parse(script.code, { ecmaVersion: 5, sourceType: 'script' });
    } catch (error) {
      const localLine = error.loc ? error.loc.line : 0;
      const baseLine = lineOf(html, script.offset);
      problems.push(`第 ${baseLine + localLine - 1} 行语法不是 ES5：${error.message}`);
    }
  }

  // 2) 扫描禁用 API 与禁用 CSS
  const banned = BANNED_APIS.concat(BANNED_CSS);
  for (const [token, reason] of banned) {
    if (target.allow.indexOf(token) >= 0) {
      continue;
    }
    const index = html.indexOf(token);
    if (index >= 0) {
      problems.push(`第 ${lineOf(html, index)} 行出现 ${token} —— ${reason}`);
    }
  }

  if (problems.length === 0) {
    console.log(`[通过] ${target.file}`);
    continue;
  }

  problemCount += problems.length;
  for (const problem of problems) {
    console.log(`[${label}] ${target.file}: ${problem}`);
  }
}

console.log('');
if (problemCount === 0) {
  console.log('全部通过，可以在老安卓上加载。');
  process.exit(0);
}

console.log(`共发现 ${problemCount} 处问题。`);
process.exit(1);
