"""Read OOXML structure for Polypod without executing document content.

This source is embedded in the Go binary and run only from an explicitly
configured Python environment. It writes plain UTF-8 text with a size limit.
"""

import json
import sys


MAX_OUTPUT = 3 << 20
MAX_SHEETS = 50
MAX_ROWS_PER_SHEET = 20000
MAX_COLUMNS_PER_SHEET = 200
MAX_NONEMPTY_CELLS = 100000
MAX_SLIDES = 500


class OutputFull(Exception):
    pass


written = 0


def emit(line):
    global written
    data = (line + "\n").encode("utf-8", errors="replace")
    if written + len(data) > MAX_OUTPUT:
        marker = b"[Saida truncada: limite de texto estruturado.]\n"
        if written + len(marker) <= MAX_OUTPUT:
            sys.stdout.buffer.write(marker)
        raise OutputFull()
    sys.stdout.buffer.write(data)
    written += len(data)


def quoted(value):
    if value is None:
        return "null"
    if hasattr(value, "isoformat"):
        value = value.isoformat()
    return json.dumps(value, ensure_ascii=False, default=str)


def xlsx(path):
    from openpyxl import load_workbook

    formulas = load_workbook(path, read_only=True, data_only=False, keep_links=False)
    values = load_workbook(path, read_only=True, data_only=True, keep_links=False)
    nonempty = 0
    try:
        names = formulas.sheetnames
        emit(f"[Abas: {len(names)}]")
        for name in names[:MAX_SHEETS]:
            sheet = formulas[name]
            cached_sheet = values[name]
            emit("[Planilha " + quoted(name) + "]")
            max_row = min(sheet.max_row or 0, MAX_ROWS_PER_SHEET)
            max_column = min(sheet.max_column or 0, MAX_COLUMNS_PER_SHEET)
            if not max_row or not max_column:
                continue
            formula_rows = sheet.iter_rows(min_row=1, max_row=max_row, min_col=1, max_col=max_column)
            cached_rows = cached_sheet.iter_rows(min_row=1, max_row=max_row, min_col=1, max_col=max_column)
            for formula_row, cached_row in zip(formula_rows, cached_rows):
                for cell, cached in zip(formula_row, cached_row):
                    if cell.value is None:
                        continue
                    nonempty += 1
                    if nonempty > MAX_NONEMPTY_CELLS:
                        emit("[Células seguintes omitidas: limite de 100000 células com conteúdo.]")
                        return
                    if cell.data_type == "f":
                        emit(f"{cell.coordinate}: fórmula {quoted(cell.value)}; valor armazenado {quoted(cached.value)}")
                    else:
                        emit(f"{cell.coordinate}: {quoted(cell.value)}")
            if (sheet.max_row or 0) > max_row or (sheet.max_column or 0) > max_column:
                emit("[Aba truncada: limite de linhas ou colunas atingido.]")
        if len(names) > MAX_SHEETS:
            emit("[Abas seguintes omitidas: limite de 50 abas.]")
    finally:
        formulas.close()
        values.close()


def docx(path):
    from docx import Document
    from docx.table import Table

    document = Document(path)
    for block in document.iter_inner_content():
        if isinstance(block, Table):
            emit("[Tabela]")
            for row in block.rows:
                emit(" | ".join(quoted(cell.text) for cell in row.cells))
            emit("[/Tabela]")
        else:
            text = block.text.strip()
            if text:
                emit("[Parágrafo] " + quoted(text))
    for number, section in enumerate(document.sections, 1):
        header = section.header
        footer = section.footer
        header_text = "\n".join(p.text for p in header.paragraphs).strip()
        footer_text = "\n".join(p.text for p in footer.paragraphs).strip()
        if header_text:
            emit(f"[Cabeçalho seção {number}] " + quoted(header_text))
        if footer_text:
            emit(f"[Rodapé seção {number}] " + quoted(footer_text))


def pptx(path):
    from pptx import Presentation

    presentation = Presentation(path)
    emit(f"[Slides: {len(presentation.slides)}]")
    for index, slide in enumerate(presentation.slides, 1):
        if index > MAX_SLIDES:
            emit("[Slides seguintes omitidos: limite de 500 slides.]")
            return
        emit(f"[Slide {index}]")
        shapes = list(slide.shapes)
        while shapes:
            shape = shapes.pop(0)
            if getattr(shape, "shape_type", None) == 6 and hasattr(shape, "shapes"):
                shapes[0:0] = list(shape.shapes)
            if getattr(shape, "has_text_frame", False):
                text = shape.text_frame.text.strip()
                if text:
                    emit("[Texto] " + quoted(text))
            if getattr(shape, "has_table", False):
                emit("[Tabela]")
                for row in shape.table.rows:
                    emit(" | ".join(quoted(cell.text) for cell in row.cells))
                emit("[/Tabela]")
        if slide.has_notes_slide:
            frame = slide.notes_slide.notes_text_frame
            if frame is not None and frame.text.strip():
                emit("[Notas] " + quoted(frame.text.strip()))


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in (".xlsx", ".docx", ".pptx"):
        print("formato estruturado inválido", file=sys.stderr)
        return 2
    try:
        {".xlsx": xlsx, ".docx": docx, ".pptx": pptx}[sys.argv[1]](sys.argv[2])
    except OutputFull:
        return 0
    except Exception as exc:
        print("falha na extração estruturada: " + type(exc).__name__, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
