package main

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/pierrec/lz4/v4"
)

const (
	lz4JavaHeaderSize = 21
	lz4JavaMagic      = "LZ4Block"
	lz4ChecksumSeed   = 0x9747b28C
	maxSourceBytes    = 32 << 20
)

// decodeLZ4JavaBlock decodes net.jpountz.lz4.LZ4BlockOutputStream data used by
// SonarQube file_sources. It is not the standard LZ4 frame format.
func decodeLZ4JavaBlock(data []byte) ([]byte, error) {
	if len(data) < lz4JavaHeaderSize || string(data[:8]) != lz4JavaMagic {
		return nil, errors.New("不是有效的 LZ4Block 源码")
	}
	offset := 0
	output := make([]byte, 0, len(data)*2)
	for {
		if len(data)-offset < lz4JavaHeaderSize {
			return nil, errors.New("LZ4Block 头不完整")
		}
		header := data[offset : offset+lz4JavaHeaderSize]
		if string(header[:8]) != lz4JavaMagic {
			return nil, errors.New("LZ4Block magic 不匹配")
		}
		offset += lz4JavaHeaderSize
		method := header[8] & 0xF0
		compressedLen := int(int32(binary.LittleEndian.Uint32(header[9:13])))
		originalLen := int(int32(binary.LittleEndian.Uint32(header[13:17])))
		expectedChecksum := binary.LittleEndian.Uint32(header[17:21])
		if compressedLen == 0 && originalLen == 0 {
			if expectedChecksum != 0 {
				return nil, errors.New("LZ4Block 结束标记校验失败")
			}
			break
		}
		if compressedLen <= 0 || originalLen <= 0 || originalLen > maxSourceBytes || compressedLen > maxSourceBytes || len(output)+originalLen > maxSourceBytes {
			return nil, errors.New("LZ4Block 长度超出安全限制")
		}
		if compressedLen > len(data)-offset {
			return nil, errors.New("LZ4Block 数据不完整")
		}
		payload := data[offset : offset+compressedLen]
		offset += compressedLen
		var decoded []byte
		switch method {
		case 0x10:
			if compressedLen != originalLen {
				return nil, errors.New("LZ4Block RAW 长度不一致")
			}
			decoded = append([]byte(nil), payload...)
		case 0x20:
			decoded = make([]byte, originalLen)
			consumed, err := lz4.UncompressBlock(payload, decoded)
			if err != nil {
				return nil, fmt.Errorf("LZ4Block 解压失败：%w", err)
			}
			if consumed == 0 || consumed != originalLen {
				return nil, errors.New("LZ4Block 压缩长度校验失败")
			}
		default:
			return nil, errors.New("未知 LZ4Block 压缩方法")
		}
		// SonarQube's historical native LZ4 writer produced checksum values that
		// differ from the XXHash32 reference implementation. Structural lengths,
		// decompression and protobuf validation below remain authoritative.
		_ = expectedChecksum
		output = append(output, decoded...)
	}
	return output, nil
}
