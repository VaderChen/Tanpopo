package llamacpp

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	ggufTypeUint8 uint32 = iota
	ggufTypeInt8
	ggufTypeUint16
	ggufTypeInt16
	ggufTypeUint32
	ggufTypeInt32
	ggufTypeFloat32
	ggufTypeBool
	ggufTypeString
	ggufTypeArray
	ggufTypeUint64
	ggufTypeInt64
	ggufTypeFloat64
)

const (
	maxGGUFMetadataEntries = 1_000_000
	maxGGUFStringBytes     = 16 * 1024 * 1024
	maxGGUFArrayElements   = 100_000_000
)

// readGGUFStringMetadata 只讀取 GGUF metadata 區，不載入 tensor 權重。
// 這讓模型能力掃描的成本與模型檔案大小無關。
func readGGUFStringMetadata(path, expectedKey string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return "", err
	}
	if string(magic[:]) != "GGUF" {
		return "", errors.New("不是 GGUF 檔案")
	}
	version, err := readGGUFUint32(reader)
	if err != nil {
		return "", err
	}
	if version < 2 || version > 3 {
		return "", fmt.Errorf("不支援的 GGUF 版本：%d", version)
	}
	if _, err := readGGUFUint64(reader); err != nil { // tensor count
		return "", err
	}
	metadataCount, err := readGGUFUint64(reader)
	if err != nil {
		return "", err
	}
	if metadataCount > maxGGUFMetadataEntries {
		return "", errors.New("GGUF metadata 數量異常")
	}

	for index := uint64(0); index < metadataCount; index++ {
		key, err := readGGUFString(reader, maxGGUFStringBytes)
		if err != nil {
			return "", err
		}
		valueType, err := readGGUFUint32(reader)
		if err != nil {
			return "", err
		}
		if key == expectedKey {
			if valueType != ggufTypeString {
				return "", fmt.Errorf("GGUF metadata %s 不是字串", expectedKey)
			}
			return readGGUFString(reader, maxGGUFStringBytes)
		}
		if err := skipGGUFValue(reader, valueType, 0); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("GGUF 缺少 metadata：%s", expectedKey)
}

func skipGGUFValue(reader io.Reader, valueType uint32, depth int) error {
	if depth > 2 {
		return errors.New("GGUF metadata array 巢狀層級過深")
	}
	if size, ok := ggufScalarSize(valueType); ok {
		return skipGGUFBytes(reader, uint64(size))
	}
	switch valueType {
	case ggufTypeString:
		length, err := readGGUFUint64(reader)
		if err != nil {
			return err
		}
		return skipGGUFBytes(reader, length)
	case ggufTypeArray:
		elementType, err := readGGUFUint32(reader)
		if err != nil {
			return err
		}
		count, err := readGGUFUint64(reader)
		if err != nil {
			return err
		}
		if count > maxGGUFArrayElements {
			return errors.New("GGUF metadata array 長度異常")
		}
		if size, ok := ggufScalarSize(elementType); ok {
			if count > ^uint64(0)/uint64(size) {
				return errors.New("GGUF metadata array 大小溢位")
			}
			return skipGGUFBytes(reader, count*uint64(size))
		}
		for index := uint64(0); index < count; index++ {
			if err := skipGGUFValue(reader, elementType, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("不支援的 GGUF metadata 型別：%d", valueType)
	}
}

func ggufScalarSize(valueType uint32) (int, bool) {
	switch valueType {
	case ggufTypeUint8, ggufTypeInt8, ggufTypeBool:
		return 1, true
	case ggufTypeUint16, ggufTypeInt16:
		return 2, true
	case ggufTypeUint32, ggufTypeInt32, ggufTypeFloat32:
		return 4, true
	case ggufTypeUint64, ggufTypeInt64, ggufTypeFloat64:
		return 8, true
	default:
		return 0, false
	}
}

func readGGUFString(reader io.Reader, maximum uint64) (string, error) {
	length, err := readGGUFUint64(reader)
	if err != nil {
		return "", err
	}
	if length > maximum {
		return "", errors.New("GGUF metadata 字串長度異常")
	}
	content := make([]byte, int(length))
	if _, err := io.ReadFull(reader, content); err != nil {
		return "", err
	}
	return string(content), nil
}

func skipGGUFBytes(reader io.Reader, count uint64) error {
	if count > uint64(^uint64(0)>>1) {
		return errors.New("GGUF metadata 大小異常")
	}
	_, err := io.CopyN(io.Discard, reader, int64(count))
	return err
}

func readGGUFUint32(reader io.Reader) (uint32, error) {
	var value uint32
	err := binary.Read(reader, binary.LittleEndian, &value)
	return value, err
}

func readGGUFUint64(reader io.Reader) (uint64, error) {
	var value uint64
	err := binary.Read(reader, binary.LittleEndian, &value)
	return value, err
}
