package computeruse

import "testing"

func TestParseOCRTSVGroupsWordsAndCoordinates(t *testing.T) {
	raw := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"5\t1\t1\t1\t1\t1\t100\t200\t50\t20\t96.0\tDigite\n" +
		"5\t1\t1\t1\t1\t2\t160\t200\t60\t20\t95.0\ta senha\n" +
		"5\t1\t1\t1\t2\t1\t300\t400\t80\t30\t90.0\tEntrar\n"
	lines := parseOCRTSV(raw)
	if len(lines) != 2 {
		t.Fatalf("len = %d, want 2", len(lines))
	}
	if lines[0].x != 100 || lines[0].y != 200 || lines[0].width != 120 || lines[0].height != 20 {
		t.Fatalf("first bounds = %d,%d,%d,%d", lines[0].x, lines[0].y, lines[0].width, lines[0].height)
	}
	if len(lines[0].words) != 2 || lines[0].words[1] != "a senha" {
		t.Fatalf("first words = %#v", lines[0].words)
	}
}
