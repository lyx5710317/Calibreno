"""Extract PDF text, recognize scans and render offline page previews."""

import argparse
from contextlib import closing
from dataclasses import dataclass
import json
import os
from pathlib import Path
import re
from statistics import median
import sys
from tempfile import NamedTemporaryFile


MODELS = {
    "Det": "PP-OCRv6_det_small.onnx",
    "Cls": "ch_ppocr_mobile_v2.0_cls_mobile.onnx",
    "Rec": "PP-OCRv6_rec_small.onnx",
}
LANGUAGES = {"zh-Hant": "chinese_cht", "ja": "japan"}
LOW_CONFIDENCE = 0.8


@dataclass(frozen=True)
class Span:
    text: str
    x: float
    y: float
    width: float
    height: float
    score: float = 1.0

    @classmethod
    def bounds(cls, text, left, top, right, bottom, score=1.0):
        return cls(text, (left + right) / 2, (top + bottom) / 2,
                   right - left, bottom - top, score)

    @property
    def top(self):
        return self.y - self.height / 2

    @property
    def bottom(self):
        return self.y + self.height / 2


def emit(**message):
    # A Windows pipe normally uses the current code page. ASCII JSON escapes
    # carry the same Unicode text on every installation.
    print(json.dumps(message, ensure_ascii=True), flush=True)


def deny_network(event, _args):
    if event in {"socket.connect", "socket.getaddrinfo", "socket.gethostbyname", "socket.sendto"}:
        raise PermissionError("The PDF worker loads local resources only.")


def create_ocr(language="auto"):
    import rapidocr

    models = Path(rapidocr.__file__).with_name("models")
    params = {
        "Global.log_level": "error",
        "EngineConfig.onnxruntime.intra_op_num_threads": min(os.cpu_count() or 1, 4),
        "EngineConfig.onnxruntime.inter_op_num_threads": 1,
    }
    if language in LANGUAGES:
        # The preset uses the same installed multilingual model and its embedded dictionary.
        params["Rec.lang_type"] = LANGUAGES[language]
    for component, name in MODELS.items():
        model = models / name
        if not model.is_file():
            raise FileNotFoundError(name)
        params[f"{component}.model_path"] = str(model)
    return rapidocr.RapidOCR(params=params)


def arrange_columns(spans):
    """Group fragments by column and attach narrow notes to nearby body text."""
    lanes = []
    for span in sorted(spans, key=lambda item: (-item.x, item.y)):
        matching = [
            lane for lane in lanes
            if min(lane[0].width, span.width) >= 0.65 * max(lane[0].width, span.width)
            and abs(lane[0].x - span.x) <= max(1, 0.5 * min(lane[0].width, span.width))
        ]
        if matching:
            nearest = min(matching, key=lambda lane: abs(lane[0].x - span.x))
            nearest.append(span)
        else:
            lanes.append([span])

    columns = [sorted(lane, key=lambda item: item.y) for lane in lanes]
    widths = [median(span.width for span in lane) for lane in columns]
    if len(columns) < 2:
        return columns
    body_width = median(sorted(widths)[len(widths) // 2:])
    notes = {}
    for index, (lane, width) in enumerate(zip(columns, widths)):
        if width >= body_width * 0.65:
            continue
        x = lane[0].x
        top = lane[0].top
        bottom = lane[-1].bottom
        owners = [
            (abs(body[0].x - x), body_index)
            for body_index, body in enumerate(columns)
            if widths[body_index] >= body_width * 0.65
            and abs(body[0].x - x) <= body_width * 1.2
            and top <= body[-1].bottom
            and bottom >= body[0].top
        ]
        if owners:
            notes[index] = min(owners)[1]
    ordered = []
    for index, lane in enumerate(columns):
        if index not in notes:
            ordered.append(lane)
            ordered.extend(columns[note] for note, owner in notes.items() if owner == index)
    return ordered


def native_band_text(spans):
    columns = arrange_columns(spans)
    stacked = sum(len(lane) for lane in columns if len(lane) > 1)
    if stacked and stacked >= len(spans) / 2:
        return "\n".join("".join(span.text for span in lane) for lane in columns)
    return ""


def horizontal_band_text(spans):
    rows = []
    for span in sorted(spans, key=lambda item: (item.y, item.x)):
        if rows and span.y - rows[-1][0].y <= min(span.height, rows[-1][0].height) / 2:
            rows[-1].append(span)
        else:
            rows.append([span])
    return "\n".join("".join(span.text for span in sorted(row, key=lambda item: item.x)) for row in rows)


def vertical_native_text(page, text_page, bands):
    from pypdfium2 import PdfiumError, raw

    spans = []
    rotation = page.get_rotation()
    for index in range(text_page.count_chars()):
        value = raw.FPDFText_GetUnicode(text_page, index)
        if not value or value > 0x10FFFF or 0xD800 <= value <= 0xDFFF:
            continue
        char = chr(value)
        if char.isspace():
            continue
        try:
            left, bottom, right, top = text_page.get_charbox(index, loose=True)
        except PdfiumError:
            continue
        bounds = {
            0: (left, -top, right, -bottom),
            90: (bottom, left, top, right),
            180: (-right, bottom, -left, top),
            270: (-top, -right, -bottom, -left),
        }[rotation]
        span = Span.bounds(char, *bounds)
        if span.width > 0 and span.height > 0:
            spans.append(span)
    if len(bands) == 1:
        return native_band_text(spans)
    left, bottom, right, top = page.get_bbox()
    origin = {0: -top, 90: left, 180: bottom, 270: -right}[rotation]
    height = page.get_size()[1]
    return "\n".join(
        native_band_text(part) or horizontal_band_text(part)
        for start, stop in bands
        if (part := [span for span in spans if origin + start * height <= span.y < origin + stop * height])
    )


def native_text(page, layout, bands):
    with closing(page.get_textpage()) as text_page:
        text = text_page.get_text_bounded().replace("\r\n", "\n").replace("\r", "\n").strip()
        if layout == "vertical-rtl" and text:
            text = vertical_native_text(page, text_page, bands) or text
    bad = sum(char == "\ufffd" or "\ue000" <= char <= "\uf8ff" for char in text)
    if bad and bad * 10 > len(text):
        return ""
    return text


def render_page(page, max_side, dpi):
    width, height = page.get_size()
    scale = min(dpi / 72, max_side / max(width, height))
    with closing(page.render(scale=scale, prefer_bgrx=False)) as bitmap:
        return bitmap.to_pil().convert("RGB")


def save_preview(page, path):
    image = render_page(page, max_side=1100, dpi=120)
    temporary = NamedTemporaryFile(dir=path.parent, prefix=f".{path.stem}-", suffix=".tmp", delete=False)
    temporary_path = Path(temporary.name)
    try:
        with temporary:
            image.save(temporary, format="JPEG", quality=83)
        temporary_path.replace(path)
    finally:
        temporary_path.unlink(missing_ok=True)
    return image


def vertical_bands(image):
    """Find lower text bands separated by whitespace and different column positions."""
    import cv2
    import numpy as np

    gray = np.asarray(image.convert("L"))
    height, width = gray.shape
    margin = round(width * 0.04)
    ink = gray[:, margin:width - margin] < 155
    density = ink.mean(axis=1)
    quiet = density <= max(0.007, np.percentile(density, 90) * 0.13)
    edges = np.flatnonzero(np.diff(quiet, prepend=False, append=False))
    cuts = [0]
    window = round(height * 0.11)
    blur = max(3, round(width * 0.012) | 1)
    for start, stop in zip(edges[::2], edges[1::2]):
        cut = (start + stop) // 2
        if start < height * 0.55 or stop > height * 0.98 or stop - start < height * 0.007:
            continue
        if cut - cuts[-1] < window / 2:
            continue
        above = ink[start - window:start].mean(axis=0)
        below = ink[stop:stop + window].mean(axis=0)
        if above.mean() < 0.012 or below.mean() < 0.01:
            continue
        left = cv2.GaussianBlur(above.reshape(1, -1), (blur, 1), 0).ravel()
        right = cv2.GaussianBlur(below.reshape(1, -1), (blur, 1), 0).ravel()
        similarity = np.dot(left, right) / (np.linalg.norm(left) * np.linalg.norm(right))
        if similarity < 0.67:
            cuts.append(cut)
    cuts.append(height)
    return [(start / height, stop / height) for start, stop in zip(cuts, cuts[1:])]


def join_fragments(lane):
    parts = [lane[0].text]
    for previous, current in zip(lane, lane[1:]):
        if current.top - previous.bottom > max(previous.width, current.width):
            parts.append("\n")
        parts.append(current.text)
    return "".join(parts)


def recognize_band(image, engine, layout):
    result = engine(image)
    if not result.txts:
        return "", []
    spans = []
    for text, box, score in zip(result.txts, result.boxes, result.scores):
        if text.strip():
            left, top = box.min(axis=0)
            right, bottom = box.max(axis=0)
            spans.append(Span.bounds(text.strip(), left, top, right, bottom, float(score)))
    groups = [[span] for span in spans]
    if layout == "vertical-rtl":
        vertical = [span for span in spans if span.height > span.width * 1.4]
        if vertical:
            top = min(span.top for span in vertical)
            horizontal = sorted((span for span in spans if span.height <= span.width * 1.4),
                                key=lambda span: (span.y, span.x))
            before = [[span] for span in horizontal if span.y < top]
            after = [[span] for span in horizontal if span.y >= top]
            groups = before + arrange_columns(vertical) + after
    low_confidence = [span.text[:100] for lane in groups for span in lane if span.score < LOW_CONFIDENCE][:25]
    return "\n".join(join_fragments(lane) for lane in groups), low_confidence


def recognize(page, engine, layout, bands):
    image = render_page(page, max_side=2400, dpi=220)
    if len(bands) == 1:
        return recognize_band(image, engine, layout)
    texts, low_confidence = [], []
    for start, stop in bands:
        crop = image.crop((0, round(start * image.height), image.width, round(stop * image.height)))
        text, uncertain = recognize_band(crop, engine, layout)
        if text:
            texts.append(text)
        low_confidence.extend(uncertain)
    return "\n\n".join(texts), low_confidence[:25]


def fold_lines(text):
    # Preserve paragraph boundaries and printed lines in reflowable XHTML.
    return re.sub(r"\n{3,}", "\n\n", "\n".join(line.rstrip() for line in text.splitlines()))


def select_pages(value, total):
    if value is None:
        return range(1, total + 1)
    selected = set()
    for part in value.split(","):
        match = re.fullmatch(r"(\d{1,4})(?:-(\d{1,4}))?", part)
        if not match:
            raise ValueError("Invalid PDF page range")
        first = int(match[1])
        last = int(match[2] or match[1])
        if not 1 <= first <= last <= total:
            raise ValueError("PDF page range outside the document")
        selected.update(range(first, last + 1))
    return sorted(selected)


def main(filename, mode, pages=None, preview_dir=None, layout="horizontal", language="auto",
         continue_on_page_error=False, image_only=False):
    try:
        import pypdfium2 as pdfium
    except Exception:
        emit(error="OCR_RUNTIME")
        return 1
    try:
        document = pdfium.PdfDocument(filename)
    except Exception:
        emit(error="PDF_OPEN")
        return 1
    with document:
        total = len(document)
        if not total or total > 3000:
            emit(error="PAGE_LIMIT" if total else "PDF_OPEN")
            return 1
        try:
            page_numbers = select_pages(pages, total)
        except ValueError:
            emit(error="PAGE_RANGE")
            return 1
        engine = None
        tolerate_page_error = continue_on_page_error or preview_dir is not None
        for number in page_numbers:
            frame = {"page": number, "total": total, "text": "", "ocr": mode == "ocr", "lowConfidence": []}
            try:
                with closing(document[number - 1]) as page:
                    preview_image = None
                    if preview_dir is not None:
                        preview = preview_dir / f"page-{number}.jpg"
                        try:
                            preview_image = save_preview(page, preview)
                            frame["image"] = True
                        except Exception:
                            if image_only:
                                frame["warning"] = "PAGE_READ"
                    if not image_only:
                        bands = [(0, 1)]
                        if layout == "vertical-rtl":
                            image = preview_image or render_page(page, max_side=1100, dpi=120)
                            bands = vertical_bands(image)
                        text = "" if mode == "ocr" else native_text(page, layout, bands)
                        frame["ocr"] = not any(char.isalnum() for char in text)
                        if frame["ocr"]:
                            if engine is None:
                                try:
                                    engine = create_ocr(language)
                                except Exception:
                                    emit(error="OCR_RUNTIME")
                                    return 1
                            text, frame["lowConfidence"] = recognize(page, engine, layout, bands)
                        frame["text"] = fold_lines(text)
                        if tolerate_page_error and "warning" not in frame and not frame["text"].strip():
                            frame["warning"] = "EMPTY"
            except Exception:
                if not tolerate_page_error:
                    emit(error="PAGE_READ")
                    return 1
                frame["warning"] = "PAGE_READ"
            emit(**frame)
    return 0


if __name__ == "__main__":
    sys.addaudithook(deny_network)
    if sys.argv[1:] == ["--check-runtime"]:
        import pypdfium2

        assert pypdfium2.PDFIUM_INFO.build > 0
        create_ocr()
        raise SystemExit(0)

    parser = argparse.ArgumentParser()
    parser.add_argument("filename")
    parser.add_argument("mode", choices=("auto", "ocr"))
    parser.add_argument("--pages")
    parser.add_argument("--preview-dir", type=Path)
    parser.add_argument("--continue-on-page-error", action="store_true")
    parser.add_argument("--image-only", action="store_true")
    parser.add_argument("--layout", choices=("horizontal", "vertical-rtl"), default="horizontal")
    parser.add_argument("--language", choices=("auto", *LANGUAGES), default="auto")
    args = parser.parse_args()
    if args.preview_dir is not None and (not args.preview_dir.is_absolute() or not args.preview_dir.is_dir()):
        parser.error("--preview-dir must be an existing absolute directory")
    if args.image_only and (args.preview_dir is None or not re.fullmatch(r"\d{1,4}", args.pages or "")):
        parser.error("--image-only requires --preview-dir and a single page number")
    raise SystemExit(main(args.filename, args.mode, args.pages, args.preview_dir, args.layout, args.language,
                          args.continue_on_page_error, args.image_only))
