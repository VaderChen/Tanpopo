#!/usr/bin/env python3
"""建立固定種子的 MMLU JSONL 抽樣，不把模型名稱納入取樣或評分。"""

from __future__ import annotations

import argparse
import csv
import io
import json
import random
import urllib.request
from pathlib import Path


DEFAULT_SOURCE = "https://openaipublic.blob.core.windows.net/simple-evals/mmlu.csv"

SUBJECT_CATEGORIES = {
    "abstract_algebra": "stem",
    "anatomy": "other",
    "astronomy": "stem",
    "business_ethics": "other",
    "clinical_knowledge": "other",
    "college_biology": "stem",
    "college_chemistry": "stem",
    "college_computer_science": "stem",
    "college_mathematics": "stem",
    "college_medicine": "other",
    "college_physics": "stem",
    "computer_security": "stem",
    "conceptual_physics": "stem",
    "econometrics": "social_sciences",
    "electrical_engineering": "stem",
    "elementary_mathematics": "stem",
    "formal_logic": "humanities",
    "global_facts": "other",
    "high_school_biology": "stem",
    "high_school_chemistry": "stem",
    "high_school_computer_science": "stem",
    "high_school_european_history": "humanities",
    "high_school_geography": "social_sciences",
    "high_school_government_and_politics": "social_sciences",
    "high_school_macroeconomics": "social_sciences",
    "high_school_mathematics": "stem",
    "high_school_microeconomics": "social_sciences",
    "high_school_physics": "stem",
    "high_school_psychology": "social_sciences",
    "high_school_statistics": "stem",
    "high_school_us_history": "humanities",
    "high_school_world_history": "humanities",
    "human_aging": "other",
    "human_sexuality": "social_sciences",
    "international_law": "humanities",
    "jurisprudence": "humanities",
    "logical_fallacies": "humanities",
    "machine_learning": "stem",
    "management": "other",
    "marketing": "other",
    "medical_genetics": "other",
    "miscellaneous": "other",
    "moral_disputes": "humanities",
    "moral_scenarios": "humanities",
    "nutrition": "other",
    "philosophy": "humanities",
    "prehistory": "humanities",
    "professional_accounting": "other",
    "professional_law": "humanities",
    "professional_medicine": "other",
    "professional_psychology": "social_sciences",
    "public_relations": "social_sciences",
    "security_studies": "social_sciences",
    "sociology": "social_sciences",
    "us_foreign_policy": "social_sciences",
    "virology": "other",
    "world_religions": "humanities",
}


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="從 OpenAI simple-evals 的 MMLU CSV 建立可重現 JSONL 抽樣。"
    )
    parser.add_argument("output", type=Path, help="輸出的 JSONL 路徑")
    parser.add_argument("--samples", type=int, default=100, help="抽樣題數，預設 100")
    parser.add_argument("--seed", type=int, default=0, help="抽樣種子，預設 0")
    parser.add_argument("--source", default=DEFAULT_SOURCE, help="MMLU CSV URL 或本機路徑")
    return parser.parse_args()


def read_source(source: str) -> list[dict[str, str]]:
    if source.startswith(("http://", "https://")):
        with urllib.request.urlopen(source, timeout=60) as response:
            text = response.read().decode("utf-8-sig")
    else:
        text = Path(source).read_text(encoding="utf-8-sig")
    rows = list(csv.DictReader(io.StringIO(text)))
    for source_row, row in enumerate(rows):
        row["_source_row"] = str(source_row)
    return rows


def main() -> None:
    arguments = parse_arguments()
    rows = read_source(arguments.source)
    if arguments.samples <= 0:
        raise SystemExit("--samples 必須大於 0")
    if arguments.samples > len(rows):
        raise SystemExit(f"要求 {arguments.samples} 題，但資料集只有 {len(rows)} 題")

    # 與 OpenAI simple-evals 相同，以固定種子直接從完整 MMLU 隨機抽樣。
    selected = random.Random(arguments.seed).sample(rows, arguments.samples)
    arguments.output.parent.mkdir(parents=True, exist_ok=True)
    with arguments.output.open("w", encoding="utf-8") as output:
        for sample_index, row in enumerate(selected, start=1):
            subject = row["Subject"]
            record = {
                "id": f"mmlu-{arguments.seed}-{sample_index:03d}",
                "source_row": int(row["_source_row"]),
                "category": SUBJECT_CATEGORIES.get(subject, "other"),
                "subject": subject,
                "question": row["Question"],
                "choices": [row["A"], row["B"], row["C"], row["D"]],
                "answer": row["Answer"],
            }
            output.write(json.dumps(record, ensure_ascii=False) + "\n")

    print(
        f"已建立 {arguments.output}：{arguments.samples} 題，seed={arguments.seed}，"
        f"來源={arguments.source}"
    )


if __name__ == "__main__":
    main()
