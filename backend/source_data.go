package main

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// decodeSonarSourceData parses the protobuf stored inside file_sources.binary_data.
// SonarQube compresses DbFileSources.Data, not plain text; Data contains repeated
// Line messages whose line number and source text are fields 1 and 2.
func decodeSonarSourceData(data []byte) ([]string, error) {
	if len(data) > maxSourceBytes {
		return nil, errors.New("源码协议数据超出安全限制")
	}
	lines := make([]string, 0, 256)
	for pos := 0; pos < len(data); {
		field, wire, value, numberValue, next, err := readProtoField(data, pos)
		if err != nil {
			return nil, err
		}
		pos = next
		_ = numberValue
		if field != 1 || wire != 2 {
			continue
		}
		number, text, err := decodeSourceLine(value)
		if err != nil {
			return nil, err
		}
		if number <= 0 || number > 1_000_000 || len(text) > maxSourceBytes {
			return nil, errors.New("源码行号或内容超出安全限制")
		}
		for len(lines) < number {
			lines = append(lines, "")
		}
		lines[number-1] = text
	}
	if len(lines) == 0 {
		return nil, errors.New("源码协议中没有可用行")
	}
	return lines, nil
}

func decodeSourceLine(data []byte) (int, string, error) {
	number := 0
	text := ""
	for pos := 0; pos < len(data); {
		field, wire, value, numberValue, next, err := readProtoField(data, pos)
		if err != nil {
			return 0, "", err
		}
		pos = next
		switch {
		case field == 1 && wire == 0:
			number = int(numberValue)
		case field == 2 && wire == 2:
			if !utf8.Valid(value) {
				return 0, "", errors.New("源码行包含非法 UTF-8")
			}
			text = string(value)
		}
	}
	if number <= 0 {
		return 0, "", errors.New("源码行缺少行号")
	}
	return number, text, nil
}

func readProtoField(data []byte, pos int) (field int, wire int, value []byte, number uint64, next int, err error) {
	tag, next, err := readVarint(data, pos)
	if err != nil {
		return 0, 0, nil, 0, 0, err
	}
	field, wire = int(tag>>3), int(tag&7)
	if field <= 0 {
		return 0, 0, nil, 0, 0, errors.New("源码协议字段无效")
	}
	switch wire {
	case 0:
		v, after, err := readVarint(data, next)
		if err != nil {
			return 0, 0, nil, 0, 0, err
		}
		return field, wire, nil, v, after, nil
	case 1:
		if next+8 > len(data) {
			return 0, 0, nil, 0, 0, errors.New("源码协议定长字段不完整")
		}
		return field, wire, data[next : next+8], 0, next + 8, nil
	case 2:
		length, after, err := readVarint(data, next)
		if err != nil {
			return 0, 0, nil, 0, 0, err
		}
		if length > uint64(len(data)-after) {
			return 0, 0, nil, 0, 0, errors.New("源码协议长度不完整")
		}
		return field, wire, data[after : after+int(length)], 0, after + int(length), nil
	case 5:
		if next+4 > len(data) {
			return 0, 0, nil, 0, 0, errors.New("源码协议定长字段不完整")
		}
		return field, wire, data[next : next+4], 0, next + 4, nil
	default:
		return 0, 0, nil, 0, 0, fmt.Errorf("不支持的源码协议 wire type %d", wire)
	}
}

func readVarint(data []byte, pos int) (uint64, int, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if pos >= len(data) {
			return 0, 0, errors.New("源码协议 varint 不完整")
		}
		b := data[pos]
		pos++
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, pos, nil
		}
	}
	return 0, 0, errors.New("源码协议 varint 溢出")
}
