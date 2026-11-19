# 对比度验收 · 方法与坑

> 2026-11-19 一轮 UI 改造中，我写的页内对比度检测器连续给出错误结论，
> 差点把「已达标」当成「不达标」去改，也差点把「真有问题的」当成已修好。
> 这篇记录正确做法与两类具体坑。

---

## 结论先行

**页内 `getComputedStyle` 检测器不可信，像素采样才是真值。**

判断依据来自同一次页面上的两个元素：

| 元素 | 页内检测器 | 真实渲染像素 |
|---|---|---|
| 「按任务分组」 | 2.29:1 ✗ | **4.95:1** ✓ |
| 「功能」（侧边栏分组） | 3.27:1 ✗ | **18.37:1** ✓ |

同一个页面、同一时刻，页内检测器把两个都判成不达标，而且数值差一个数量级。

---

## 坑一：色彩空间不是 rgb()

这台机器上 Tailwind / 主题 token 算出来的颜色是 **`lab()` / `oklab()` / `oklch()`**，
不是 `rgb()`。

```js
getComputedStyle(el).color
// "oklab(0.15 -0.00000148267 0.00000350922 / 0.6)"
```

用 `str.match(/rgba?\(([^)]+)\)/)` 这种正则去解析，**直接解析失败**：

- 返回 `null` → 该元素被跳过 → 统计出来「测 0 处」，看着像全绿
- 或者走进兜底分支 → 白底假定 → 算出 1:1

**正确的取值方式**：用 canvas 让浏览器自己换算，任何色彩空间都能吃。

```js
const ctx = document.createElement('canvas').getContext('2d');
ctx.canvas.width = ctx.canvas.height = 1;
function toRgb(str){
  ctx.fillStyle = '#ff00ff';   // 先污染，解析失败才能看出来
  ctx.fillStyle = str;
  ctx.fillRect(0,0,1,1);
  const d = ctx.getImageData(0,0,1,1).data;
  return `rgb(${d[0]},${d[1]},${d[2]})`;
}
```

注意两个细节：

1. **必须先填一个醒目的哨兵色**（`#ff00ff`），否则解析失败时 `fillStyle` 保留上一次的值，会静默给出错误答案
2. `lab(... / 0.6)` 这种带 alpha 的写法，canvas 只返回 RGB，**alpha 要自己从字符串里抠**，抠不到就当 1 会高估对比度

---

## 坑二：文字节点所在的元素 ≠ 实际绘制的元素

`getComputedStyle` 返回的是**该元素自身**的样式，但文字可能被**子元素**绘制成另一个颜色，
或者被祖先的 `opacity` / `filter` 影响。

实测：「功能」这个文字节点的 `getComputedStyle().color` 是 `oklab(0.2 …)`，
但屏幕上的实际像素是 `rgb(11,11,11)` —— 两者差得很远，算出来的对比度自然也是错的。

DOM 遍历法要正确，得同时处理：颜色继承、`opacity` 累积、`background-image`、
伪元素、`mix-blend-mode`。这个复杂度已经超过「顺手写个检查脚本」的合理范围。

---

## 正确做法：截图像素采样

```python
# 1) 浏览器里拿元素位置
rect = el.getBoundingClientRect()   # {x, y, width, height}

# 2) 用 CDP 按位置截图（scale=4 抗锯齿）
cdp('Page.captureScreenshot', format='png',
    clip={'x': rect.x, 'y': rect.y, 'width': rect.w, 'height': rect.h, 'scale': 4})

# 3) 像素直方图：出现最多的颜色 = 背景，最暗像素 = 文字
from PIL import Image
import collections
im = Image.open(path).convert('RGB')
px = list(im.getdata())
bg = collections.Counter(px).most_common(1)[0][0]
fg = min(px, key=lum)

ratio = (max(lum(bg), lum(fg)) + 0.05) / (min(lum(bg), lum(fg)) + 0.05)
```

这套方法给出的是**屏幕上的真实对比度**，不受色彩空间与层叠影响。

代价是每个元素一次截图调用，**全站扫一遍很慢**。实用策略：

- **全站粗筛**：用页内检测器找出可疑元素（接受它有假阳性）
- **逐个确认**：只对可疑元素做像素采样，以像素结果为准
- **绝不用页内检测器的数字直接下结论**

---

## 坑三：PIL 被 PYTHONPATH 污染

```
ImportError: cannot import name '_imaging' from 'PIL'
```

Hermes 的 venv 通过 `PYTHONPATH` 泄漏到子进程。跑 Pillow 必须：

```bash
env -u PYTHONPATH ~/.hermes/venvs/ml/bin/python script.py
```

---

## 什么时候不用担心检测器

纯算术判断不需要渲染，可以直接算：

- 已知前景色 `#34d399` + 已知背景色 `#ffffff` → 1.9:1

这类「两个色值都是常数」的场景，直接套 WCAG 公式即可，没有色彩空间与层叠问题。
本仓库 `scripts/fix-semantic-contrast.py` 处理的就是这一类：**静态类名 → 已知色值**。

判断分界：

| 场景 | 方法 |
|---|---|
| 静态工具类（`text-emerald-400` on white） | 算术即可 |
| 主题 token（CSS 变量、可能 alpha/继承） | **必须像素采样** |

---

*2026-11-19*
