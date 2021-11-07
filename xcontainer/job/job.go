package job

// Do是多线程调用
type Do interface {
	DoJob() Done
}

// Done是主线程调用
type Done interface {
	DoReturn()
}
