package channel

type Channel interface {
	Write(interface{})
	TryRead() (interface{}, bool)
	Read() (interface{}, bool)
	Close()
	Len() int
}
