package voice

import (
	"bytes"
	"fmt"
	"io"
)

func ExtractOpusPackets(data []byte) ([][]byte, error) {
	reader := bytes.NewReader(data)
	var packets [][]byte
	var current []byte

	for {
		header := make([]byte, 27)
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		if string(header[:4]) != "OggS" {
			return nil, fmt.Errorf("invalid ogg capture pattern")
		}

		segmentCount := int(header[26])
		segmentTable := make([]byte, segmentCount)
		if _, err := io.ReadFull(reader, segmentTable); err != nil {
			return nil, err
		}

		total := 0
		for _, l := range segmentTable {
			total += int(l)
		}

		pageData := make([]byte, total)
		if _, err := io.ReadFull(reader, pageData); err != nil {
			return nil, err
		}

		cursor := 0
		for _, l := range segmentTable {
			if cursor+int(l) > len(pageData) {
				return nil, fmt.Errorf("invalid segment length")
			}

			current = append(current, pageData[cursor:cursor+int(l)]...)
			cursor += int(l)

			if l < 255 {
				packet := make([]byte, len(current))
				copy(packet, current)
				packets = append(packets, packet)
				current = current[:0]
			}
		}
	}

	if len(current) > 0 {
		packet := make([]byte, len(current))
		copy(packet, current)
		packets = append(packets, packet)
	}

	if len(packets) == 0 {
		return nil, fmt.Errorf("no opus packets found")
	}

	return packets, nil
}
