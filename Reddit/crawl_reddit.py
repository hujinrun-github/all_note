#!/usr/bin/env python3
"""
Reddit 效率工具调研爬虫
目标：收集用户痛点、抱怨、需求、替代品讨论
"""
import requests
import json
import time
import os

OUTPUT_DIR = os.path.dirname(os.path.abspath(__file__))
HEADERS = {"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) ResearchBot/1.0"}

# 要搜索的关键词组合
QUERIES = [
    # 痛点/抱怨
    "complaint OR rant OR frustrated OR terrible OR annoying OR sucks",
    "overpriced OR bloated OR slow OR buggy OR crash",
    "hate OR garbage OR worst OR kill OR dying",
    "switching from OR replaced OR alternative to OR moving away",
    # 需求
    "\"wish there was\" OR \"I wish\" OR \"I need\"",
    "\"feature request\" OR \"missing feature\" OR \"please add\"",
    "\"does anyone know\" OR \"looking for\" OR \"recommend me\"",
    "\"what do you use\" OR \"your workflow\" OR \"daily stack\"",
]

SUBREDDITS = [
    "productivity",
    "software",
    "Notion",
    "todoist",
    "clickup",
    "obsidianmd",
    "Evernote",
    "gtd",
    "selfhosted",
    "digitalminimalism",
    "SaaS",
    "PKMS",
]

all_posts = []

def search_subreddit(subreddit, query, limit=25):
    """Search a subreddit, return posts"""
    url = f"https://old.reddit.com/r/{subreddit}/search.json"
    params = {
        "q": query,
        "sort": "top",
        "restrict_sr": "on",
        "limit": limit,
        "t": "year",
    }
    try:
        r = requests.get(url, headers=HEADERS, params=params, timeout=30)
        if r.status_code != 200:
            print(f"  ❌ r/{subreddit} HTTP {r.status_code}")
            return []
        data = r.json()
        posts = data.get("data", {}).get("children", [])
        results = []
        for p in posts:
            d = p["data"]
            results.append({
                "subreddit": subreddit,
                "title": d["title"],
                "score": d["score"],
                "num_comments": d["num_comments"],
                "url": f"https://reddit.com{d['permalink']}",
                "selftext": d.get("selftext", "")[:500],
                "created_utc": d["created_utc"],
            })
        return results
    except Exception as e:
        print(f"  ❌ r/{subreddit} Error: {e}")
        return []


def main():
    print("=" * 60)
    print("🔍 Reddit 效率工具调研爬虫")
    print("=" * 60)

    for subreddit in SUBREDDITS:
        print(f"\n📂 r/{subreddit} ...")
        for query in QUERIES:
            # 缩写 query 用于显示
            short_q = query[:60].replace('"', '').replace("OR", "/")
            print(f"  🔎 {short_q}...", end=" ", flush=True)
            posts = search_subreddit(subreddit, query)
            for post in posts:
                if post not in all_posts:  # 去重
                    post["query_category"] = "pain" if any(w in query for w in ["complaint", "rant", "hate", "sucks", "overpriced", "bloated", "slow", "buggy", "crash", "switching", "alternative", "worst", "dying"]) else "need"
                    all_posts.append(post)
            print(f"{len(posts)} results")
            time.sleep(2)  # 限速，避免被 Reddit 封

    # 按分数排序
    all_posts.sort(key=lambda x: x["score"], reverse=True)

    # 保存
    output_file = os.path.join(OUTPUT_DIR, "reddit_raw.json")
    with open(output_file, "w", encoding="utf-8") as f:
        json.dump(all_posts, f, ensure_ascii=False, indent=2)

    print(f"\n✅ 完成！共收集 {len(all_posts)} 条帖子，保存到 {output_file}")

    # 简单统计
    pain_count = sum(1 for p in all_posts if p["query_category"] == "pain")
    need_count = sum(1 for p in all_posts if p["query_category"] == "need")
    print(f"   痛点(pain): {pain_count} 条")
    print(f"   需求(need): {need_count} 条")

    # 打印 TOP 10 痛点
    print("\n📌 TOP 10 高分帖（可能是痛点相关）:")
    for i, p in enumerate(all_posts[:10]):
        print(f"  {i+1}. [{p['score']}↑] r/{p['subreddit']} — {p['title'][:100]}")
        print(f"     {p['url']}")


if __name__ == "__main__":
    main()
