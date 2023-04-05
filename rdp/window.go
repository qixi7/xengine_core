package rdpkit

type Window struct {
	v    [rttWindow]int64
	i, n int
	min  int64
}

func (w *Window) Append(value int64) {
	if value < 0 {
		panic("negative RTT")
	}
	w.v[w.i] = value
	w.i = (w.i + 1) % rttWindow
	if w.i > w.n {
		w.n = w.i
	}
	if value < w.min {
		w.min = value
	} else {
		w.min = -1
	}
}

func (w *Window) Min() int64 {
	if w.min > 0 {
		return w.min
	}
	w.min = w.v[0]
	for i := 1; i < w.n; i++ {
		if w.v[i] < w.min {
			w.min = w.v[i]
		}
	}
	return w.min
}
