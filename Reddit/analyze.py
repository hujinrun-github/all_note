#!/usr/bin/env python3
"""分析 Reddit 调研数据，提取效率工具相关的痛点和需求"""
import json
import os
from collections import Counter
from datetime import datetime

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
INPUT_FILE = os.path.join(SCRIPT_DIR, "reddit_raw.json")
OUTPUT_FILE = os.path.join(SCRIPT_DIR, "reddit_analysis.md")

# 加载数据
with open(INPUT_FILE, "r", encoding="utf-8") as f:
    posts = json.load(f)

print(f"原始数据: {len(posts)} 条")

# 1. 按 URL 去重（保留最高分版本）
seen = {}
deduped = []
for p in posts:
    url = p["url"]
    if url not in seen or p["score"] > seen[url]["score"]:
        seen[url] = p
deduped = list(seen.values())
deduped.sort(key=lambda x: x["score"], reverse=True)
print(f"去重后: {len(deduped)} 条")

# 2. 过滤效率/生产力工具相关（排除纯生活类、纯自托管硬件等）
productivity_keywords = [
    "notion", "todoist", "obsidian", "evernote", "clickup", "taskade",
    "note", "task", "todo", "project management", "kanban", "gtd",
    "productivity", "efficiency", "workflow", "automation",
    "calendar", "reminder", "planner", "organize", "pkm",
    "focus", "distraction", "time tracking", "pomodoro",
    "ai tool", "chatgpt", "copilot", "cursor",
    "app", "tool", "software", "extension", "plugin",
    "subscription", "free alternative", "open source",
]

def is_relevant(post):
    text = (post["title"] + " " + post["selftext"]).lower()
    sub = post["subreddit"].lower()
    # 效率工具相关 subreddit 自动通过
    if sub in ["productivity", "notion", "todoist", "clickup", "obsidianmd", "evernote", "gtd", "pkms", "digitalminimalism"]:
        return True
    # 其他 sub 需要关键词匹配
    score = sum(1 for kw in productivity_keywords if kw in text)
    return score >= 2

relevant = [p for p in deduped if is_relevant(p)]
print(f"效率工具相关: {len(relevant)} 条")

# 3. 按 subreddit 统计
sub_counts = Counter(p["subreddit"] for p in relevant)
print("\n📂 按 subreddit 分布:")
for sub, cnt in sub_counts.most_common():
    print(f"  r/{sub}: {cnt}")

# 4. 提取关键痛点和需求
pain_posts = [p for p in relevant if p["query_category"] == "pain"]
need_posts = [p for p in relevant if p["query_category"] == "need"]
print(f"\n痛点帖: {len(pain_posts)}, 需求帖: {len(need_posts)}")

# 5. 生成报告
report = []
report.append("# Reddit 效率工具调研报告")
report.append(f"\n> 数据采集时间: {datetime.now().strftime('%Y-%m-%d')}")
report.append(f"> 原始数据 {len(posts)} 条 → 去重 {len(deduped)} 条 → 效率工具相关 {len(relevant)} 条")
report.append(f"> 痛点帖 {len(pain_posts)} 条 | 需求帖 {len(need_posts)} 条\n")

# === 高频痛点分析 ===
report.append("\n---\n\n## 🔥 高频痛点 TOP 15\n")
report.append("以下是 Reddit 上用户对效率工具最强烈的抱怨和不满：\n")

pain_sorted = sorted(pain_posts, key=lambda x: (x["score"] + x["num_comments"] * 2), reverse=True)
shown = set()
count = 0
for p in pain_sorted[:30]:
    title = p["title"]
    if title in shown:
        continue
    shown.add(title)
    count += 1
    engagement = p["score"] + p["num_comments"]
    report.append(f"### {count}. [{p['score']}↑, {p['num_comments']}💬] {title}\n")
    report.append(f"   r/{p['subreddit']} | 🔗 {p['url']}")
    if p['selftext']:
        # 提取前200字
        snippet = p['selftext'][:250].replace('\n', ' ').strip()
        report.append(f"\n   > {snippet}...")
    report.append("")

# === Notion 专题 ===
notion_posts = [p for p in relevant if "notion" in (p["title"] + p["selftext"]).lower()]
report.append("\n---\n\n## 📋 Notion 用户抱怨专题\n")
for p in sorted(notion_posts, key=lambda x: x["score"], reverse=True)[:10]:
    report.append(f"- **[{p['score']}↑]** {p['title'][:120]}")
    report.append(f"  🔗 {p['url']}")
    if p['selftext']:
        snippet = p['selftext'][:150].replace('\n', ' ').strip()
        report.append(f"  > {snippet}...")
    report.append("")

# === 新兴需求 ===
report.append("\n---\n\n## 💡 用户最渴望的功能 / 未被满足的需求\n")
need_sorted = sorted(need_posts, key=lambda x: (x["score"] + x["num_comments"] * 2), reverse=True)
shown = set()
count = 0
for p in need_sorted[:30]:
    title = p["title"]
    if title in shown:
        continue
    shown.add(title)
    count += 1
    report.append(f"### {count}. [{p['score']}↑, {p['num_comments']}💬] {title}\n")
    report.append(f"   r/{p['subreddit']} | 🔗 {p['url']}")
    if p['selftext']:
        snippet = p['selftext'][:250].replace('\n', ' ').strip()
        report.append(f"\n   > {snippet}...")
    report.append("")

# === 趋势：从什么迁移到什么 ===
report.append("\n---\n\n## 🔄 工具迁移趋势\n")
migration_posts = [p for p in relevant 
    if any(w in (p["title"] + p["selftext"]).lower() 
           for w in ["switching", "moved from", "migrated", "replaced", "alternative", "instead of"])]

migration_map = Counter()
for p in migration_posts[:40]:
    text = (p["title"] + " " + p["selftext"]).lower()
    pairs = []
    if "notion" in text and "obsidian" in text:
        pairs.append("Notion → Obsidian")
    if "evernote" in text and ("notion" in text or "obsidian" in text):
        pairs.append("Evernote → Notion/Obsidian")
    if "clickup" in text and ("notion" in text or "linear" in text):
        pairs.append("ClickUp → Notion/Linear")
    if "todoist" in text and ("notion" in text or "obsidian" in text or "things" in text):
        pairs.append("Todoist → Notion/Things")
    for pair in pairs:
        migration_map[pair] += 1

report.append("| 迁移方向 | 出现次数 |")
report.append("|---|---|")
for pair, cnt in migration_map.most_common(10):
    report.append(f"| {pair} | {cnt} |")

# === 核心洞察 ===
report.append("\n---\n\n## 🎯 核心洞察 & 产品机会\n")

insights = [
    ("Notion 逃离潮", 
     "大量用户从 Notion 迁移到 Obsidian 等本地化工具。核心抱怨：AI 强塞、App 打开慢、数据不在本地、订阅疲劳。\n"
     "**机会**: 做一款「Notion 替代品」，重点差异化：本地优先 + 快速加载 + 可选 AI，不要强推 AI 功能。"),
    ("订阅疲劳症", 
     "用户对 SaaS 订阅越来越抗拒。频繁出现 'tired of subscriptions'、'looking for free alternative'、'self-host' 等表述。\n"
     "**机会**: 买断制或一次付费的产品模式有吸引力；或 freemium 但核心功能永远免费。"),
    ("AI 反感情绪", 
     "多个社区（Notion、Obsidian、Selfhosted）都出现强烈的 AI 疲劳和反感。'AI slop' 成为热词，用户不喜欢 AI 生成的低质量内容。\n"
     "**机会**: 如果你的产品用 AI，必须「无声融入」而非「硬推」。用户要的是「AI 辅助我工作」而非「AI 代替我思考」。"),
    ("工具太多，整合不够", 
     "用户痛恨在多个工具之间切换。典型抱怨：日程在 Google Calendar，任务在 Todoist，笔记在 Obsidian，团队协作在 Notion——数据割裂。\n"
     "**机会**: 做「All-in-one 轻量版」——不需要大而全，但要打通核心流程（笔记 + 任务 + 日历）。"),
    ("性能焦虑", 
     "Notion App 慢、Evernote 臃肿、ClickUp 复杂——用户对「快」的需求极高。'bloated' 是最常见负面词之一。\n"
     "**机会**: 把「极致性能」作为核心卖点，秒开、离线可用、本地优先。"),
    ("注意力管理需求爆发", 
     "r/productivity 和 r/digitalminimalism 中，「手机成瘾」「分心」「屏幕时间」是最热话题。用户渴望「帮我管住自己」的工具。\n"
     "**机会**: 效率工具不应只是「组织任务」，更应该是「帮用户保持专注」。考虑内置 Focus Mode、网站屏蔽、手机使用追踪。"),
    ("PKM 本地化趋势", 
     "Obsidian 的成功证明：用户愿意为本地 Markdown 文件 + 强大插件的模式买单。数据主权（data sovereignty）是核心卖点。\n"
     "**机会**: 基于本地文件的产品（Markdown 优先）有自然 SEO 优势，因为笔记本身就是搜索友好的文本。"),
    ("团队协作摩擦", 
     "个人效率工具很好用，但一到团队协作就崩塌。用户想要「个人用着爽，分享给团队也丝滑」的体验。\n"
     "**机会**: 做一款「个人优先，团队可选」的产品。先做好个人体验，再加团队功能。"),
    ("开源自托管需求", 
     "r/selfhosted 活跃度极高，大量用户愿意折腾，只要数据在自己手里。Bitwarden、Jellyfin 等替代商业服务成为趋势。\n"
     "**机会**: 开源核心 + 付费托管服务（像 Bitwarden / Plausible 模式），既满足自托管用户又赚钱。"),
    ("移动端体验糟糕", 
     "多款工具（Notion、ClickUp）移动端被吐槽'太慢''没法用'。用户需要真正好用的移动端 App。\n"
     "**机会**: 移动端做得好是巨大的差异化优势。支持快速捕获（quick capture）、离线编辑、同步可靠。"),
]

for i, (title, detail) in enumerate(insights, 1):
    report.append(f"### {i}. {title}\n")
    report.append(f"{detail}\n")

# 保存
with open(OUTPUT_FILE, "w", encoding="utf-8") as f:
    f.write("\n".join(report))

print(f"\n✅ 分析报告已保存到: {OUTPUT_FILE}")
print(f"   共 {len(pain_posts)} 条痛点 + {len(need_posts)} 条需求")
