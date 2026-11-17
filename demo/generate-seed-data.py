#!/usr/bin/env python3
"""
Generates synthetic scans and the matching jobs/meta.json layout
doxie-scanner's storage package expects, so the demo has real-looking scan
history without needing the physical scanner. Content is entirely made up
(no real scanned documents involved).

Besides two plain scans, it writes "demo-receipts": a receipt and a business
card lying tilted on the scanner glass, and a letter fed in sideways. These are
the RAW pages; record-demo.sh then runs the app's own auto-crop and
auto-rotate over them through the API, so what the recording shows has really
been trimmed, straightened and turned upright by the app, not drawn that way.

Usage:
    python3 generate-seed-data.py <target-data-dir>
"""
import json
import sys
from pathlib import Path

import random

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

BACKING = (196, 199, 198)  # the grey lid of the scanner


def on_glass(paper, angle, canvas=(1700, 2200), offset=(0, 0)):
    """Lay `paper` tilted by `angle` degrees on a grey scanner-lid backdrop."""
    rnd = random.Random(7)
    bg = Image.new("RGB", canvas, BACKING)
    px = bg.load()
    for _ in range(canvas[0] * canvas[1] // 40):  # faint sensor noise
        x, y = rnd.randrange(canvas[0]), rnd.randrange(canvas[1])
        v = rnd.randint(-4, 4)
        px[x, y] = tuple(max(0, min(255, c + v)) for c in BACKING)
    rot = paper.convert("RGBA").rotate(angle, expand=True, resample=Image.BICUBIC)
    x = (canvas[0] - rot.width) // 2 + offset[0]
    y = (canvas[1] - rot.height) // 2 + offset[1]
    bg.paste(rot, (x, y), rot)
    return bg


def receipt_paper():
    img = Image.new("RGB", (640, 1250), (250, 249, 245))
    d = ImageDraw.Draw(img)
    d.text((320, 70), "RIVERTOWN SUPPLY", font=font(34, bold=True), fill=(25, 25, 25), anchor="mm")
    d.text((320, 115), "412 Mill Street", font=font(22), fill=(70, 70, 70), anchor="mm")
    d.line((40, 150, 600, 150), fill=(150, 150, 150), width=2)
    y = 190
    for item, price in [("Packing tape x12", "54.00"), ("Boxes, medium x40", "48.00"),
                        ("Shipping labels x6", "48.00"), ("Bubble wrap x3", "44.25"),
                        ("Box cutter", "7.80"), ("Marker set", "11.40")]:
        d.text((40, y), item, font=font(24), fill=(30, 30, 30))
        d.text((600, y), price, font=font(24), fill=(30, 30, 30), anchor="ra")
        y += 52
    d.line((40, y + 10, 600, y + 10), fill=(150, 150, 150), width=2)
    d.text((40, y + 40), "TOTAL", font=font(30, bold=True), fill=(20, 20, 20))
    d.text((600, y + 40), "213.45", font=font(30, bold=True), fill=(20, 20, 20), anchor="ra")
    d.text((320, y + 150), "Thank you!", font=font(26), fill=(70, 70, 70), anchor="mm")
    for i in range(36):  # barcode
        w = 3 if i % 3 else 6
        d.rectangle([90 + i * 12, 1090, 90 + i * 12 + w, 1190], fill=(20, 20, 20))
    return img


def card_paper():
    img = Image.new("RGB", (1050, 600), (246, 248, 252))
    d = ImageDraw.Draw(img)
    d.rectangle([0, 0, 28, 600], fill=(44, 62, 80))
    d.text((80, 120), "Dana Whitfield", font=font(54, bold=True), fill=(25, 35, 50))
    d.text((80, 195), "Operations Coordinator", font=font(30), fill=(70, 80, 95))
    d.line((80, 260, 500, 260), fill=(44, 62, 80), width=3)
    d.text((80, 320), "dana.whitfield@example.test", font=font(28), fill=(50, 60, 75))
    d.text((80, 370), "(555) 010-2231", font=font(28), fill=(50, 60, 75))
    d.text((80, 420), "412 Willow Creek Rd, Springvale", font=font(28), fill=(50, 60, 75))
    return img


def sideways_letter():
    """A full text page, fed in on its side (rotated a quarter turn)."""
    img = Image.new("RGB", (1275, 1650), (252, 251, 248))
    d = ImageDraw.Draw(img)
    d.text((110, 110), "Springvale Utilities", font=font(46, bold=True), fill=(25, 25, 25))
    d.text((110, 175), "Account statement - June 2026", font=font(30), fill=(70, 70, 70))
    d.line((110, 230, 1165, 230), fill=(170, 170, 170), width=2)
    body = (
        "Dear customer, thank you for your payment. Your balance for the\n"
        "billing period ending June 30 is shown below. Please keep this\n"
        "statement for your records. If you have any questions about your\n"
        "usage or the charges on your account, contact our customer service\n"
        "team during normal business hours and quote your account number.\n\n"
        "Water service charge .......................................... 41.20\n"
        "Sewer service charge .......................................... 28.75\n"
        "Trash and recycling ............................................ 19.00\n"
        "Previous balance ................................................ 0.00\n"
        "Payment received - thank you ............................... -88.95\n\n"
        "Amount due ...................................................... 0.00\n\n"
        "Our offices are closed on public holidays. Online payments are\n"
        "accepted at any time through the customer portal, and automatic\n"
        "payments can be set up with a bank account or a debit card."
    )
    y = 290
    for line in body.split("\n"):
        d.text((110, y), line, font=font(28), fill=(35, 35, 35))
        y += 46
    return img.rotate(-90, expand=True)


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
    write_job(
        root, "demo-receipts", "Scan 2026-07-21 10:42",
        "2026-07-21T10:42:00Z",
        [on_glass(receipt_paper(), 3.2, offset=(-40, 20)),
         on_glass(card_paper(), -3, offset=(60, -120)),
         sideways_letter()],
    )
    print(f"Seed data written to {root}")


if __name__ == "__main__":
    main()
