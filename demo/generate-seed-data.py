#!/usr/bin/env python3
"""
Generates a couple of synthetic, letter-shaped "document" images and the
matching jobs/meta.json layout doxie-scanner's storage package expects,
so the demo/integration test has real-looking scan history to click
through without needing the physical scanner. Content is entirely made
up (no real scanned documents involved).

Usage:
    python3 generate-seed-data.py <target-data-dir>
"""
import json
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

PAGE_W, PAGE_H = 850, 1100  # roughly US letter aspect ratio, demo-sized (not full 300 DPI)
FONT_PATH = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
FONT_BOLD_PATH = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"


def font(size, bold=False):
    try:
        return ImageFont.truetype(FONT_BOLD_PATH if bold else FONT_PATH, size)
    except OSError:
        return ImageFont.load_default(size=size)


def blank_page():
    return Image.new("RGB", (PAGE_W, PAGE_H), (255, 255, 255))


def invoice_page_1():
    img = blank_page()
    d = ImageDraw.Draw(img)
    d.text((60, 60), "Rivertown Supply Co.", font=font(30, bold=True), fill=(20, 20, 20))
    d.text((60, 100), "Invoice #1042", font=font(20), fill=(60, 60, 60))
    d.line((60, 140, 790, 140), fill=(180, 180, 180), width=2)

    rows = [
        ("Item", "Qty", "Price"),
        ("Packing tape (heavy duty)", "12", "$4.50"),
        ("Corrugated boxes, medium", "40", "$1.20"),
        ("Shipping labels, roll of 250", "6", "$8.00"),
        ("Bubble wrap, 100ft", "3", "$14.75"),
    ]
    y = 180
    for i, (item, qty, price) in enumerate(rows):
        d.text((60, y), item, font=font(18, bold=(i == 0)), fill=(20, 20, 20))
        d.text((620, y), qty, font=font(18, bold=(i == 0)), fill=(20, 20, 20))
        d.text((710, y), price, font=font(18, bold=(i == 0)), fill=(20, 20, 20))
        y += 36
        if i == 0:
            d.line((60, y - 8, 790, y - 8), fill=(200, 200, 200), width=1)

    d.line((60, y + 20, 790, y + 20), fill=(180, 180, 180), width=2)
    d.text((600, y + 40), "Total: $312.60", font=font(20, bold=True), fill=(20, 20, 20))

    d.text((60, PAGE_H - 80), "Page 1 of 2", font=font(14), fill=(140, 140, 140))
    return img


def invoice_page_2():
    img = blank_page()
    d = ImageDraw.Draw(img)
    d.text((60, 60), "Terms & Conditions", font=font(26, bold=True), fill=(20, 20, 20))
    d.line((60, 100, 790, 100), fill=(180, 180, 180), width=2)

    paragraph = (
        "Payment is due within 30 days of the invoice date. Late payments\n"
        "are subject to a 1.5% monthly service charge. Goods remain the\n"
        "property of Rivertown Supply Co. until payment is received in\n"
        "full. Returns are accepted within 14 days with original packaging\n"
        "and proof of purchase. Shipping costs on returned items are the\n"
        "responsibility of the customer unless the return is due to a\n"
        "defect or shipping error on our part."
    )
    y = 140
    for line in paragraph.split("\n"):
        d.text((60, y), line, font=font(17), fill=(40, 40, 40))
        y += 30

    d.text((60, PAGE_H - 80), "Page 2 of 2", font=font(14), fill=(140, 140, 140))
    return img


def cover_letter_page():
    img = blank_page()
    d = ImageDraw.Draw(img)
    d.text((60, 60), "Dana Whitfield", font=font(24, bold=True), fill=(20, 20, 20))
    d.text((60, 92), "412 Willow Creek Rd, Springvale", font=font(16), fill=(80, 80, 80))
    d.text((60, 114), "dana.whitfield@example.test", font=font(16), fill=(80, 80, 80))

    d.text((60, 170), "July 15, 2026", font=font(16), fill=(60, 60, 60))
    d.text((60, 210), "To the Hiring Committee,", font=font(18), fill=(20, 20, 20))

    paragraph = (
        "I'm writing to express interest in the operations coordinator\n"
        "role. Over the past six years I've managed logistics for a\n"
        "regional distribution network, and I'd welcome the chance to\n"
        "bring that experience to your team.\n"
        "\n"
        "Thank you for your time and consideration.\n"
        "\n"
        "Sincerely,\n"
        "Dana Whitfield"
    )
    y = 250
    for line in paragraph.split("\n"):
        d.text((60, y), line, font=font(17), fill=(40, 40, 40))
        y += 30

    return img


def write_job(root: Path, job_id: str, name: str, created_at: str, pages):
    job_dir = root / "jobs" / job_id
    pages_dir = job_dir / "pages"
    pages_dir.mkdir(parents=True, exist_ok=True)

    meta_pages = []
    for i, img in enumerate(pages, start=1):
        filename = f"page-{i:03d}.png"
        img.save(pages_dir / filename)
        meta_pages.append({
            "index": i,
            "file": filename,
            "widthPx": img.width,
            "heightPx": img.height,
        })

    meta = {
        "id": job_id,
        "name": name,
        "driver": "doxie-dx400",
        "createdAt": created_at,
        "completedAt": created_at,
        "status": "completed",
        "duplex": False,
        "dpi": 300,
        "pageCount": len(meta_pages),
        "pages": meta_pages,
    }
    (job_dir / "meta.json").write_text(json.dumps(meta, indent=2))


def main():
    if len(sys.argv) != 2:
        sys.exit(f"Usage: {sys.argv[0]} <target-data-dir>")
    root = Path(sys.argv[1])

    write_job(
        root, "demo-invoice", "Q3 Invoice — Rivertown Supply",
        "2026-07-10T09:15:00Z",
        [invoice_page_1(), invoice_page_2()],
    )
    write_job(
        root, "demo-letter", "Cover Letter Draft",
        "2026-07-12T14:30:00Z",
        [cover_letter_page()],
    )
    print(f"Seed data written to {root}")


if __name__ == "__main__":
    main()
