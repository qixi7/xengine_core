package codec

type Codec interface {
	Encode(dst, src []byte) ([]byte, bool)
	Decode(dst, src []byte) ([]byte, error)
}

func New(encode string) Codec {
	switch encode {
	case "snappy":
		// todo. snappy
	case "lz4":
		// todo. 后续做压缩时候再考虑, 主要是mac交叉编译linux不支持cgo
		//return &LZ4C{}
		return nil
	default:
		return nil
	}
	return nil
}
