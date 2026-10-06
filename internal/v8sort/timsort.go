// Package v8sort is V8's Array.prototype.sort (third_party/v8/builtins/
// array-sort.tq): TimSort with binary insertion for short runs and galloping
// merges. With a consistent comparator any stable sort gives the same order,
// but localeCompare is not always transitive (ICU's identical-prefix skip),
// so the order then depends on exactly which comparisons are made; this port
// makes the same calls in the same order as V8.
package v8sort

const minGallopWins = 7

type sorter[T any] struct {
	work      []T
	cmp       func(a, b T) int
	minGallop int
	runBase   []int
	runLen    []int
}

// Sort sorts s in place with V8's algorithm; cmp(a, b) < 0 means a sorts
// before b (the comparefn result, only its sign matters).
func Sort[T any](s []T, cmp func(a, b T) int) {
	if len(s) < 2 {
		return
	}
	st := &sorter[T]{work: s, cmp: cmp, minGallop: minGallopWins}
	low, remaining := 0, len(s)
	minRun := minRunLength(remaining)
	for remaining != 0 {
		runLen := st.countAndMakeRun(low, low+remaining)
		if runLen < minRun {
			forced := min(minRun, remaining)
			st.binaryInsertionSort(low, low+runLen, low+forced)
			runLen = forced
		}
		st.runBase = append(st.runBase, low)
		st.runLen = append(st.runLen, runLen)
		st.mergeCollapse()
		low += runLen
		remaining -= runLen
	}
	st.mergeForceCollapse()
}

func minRunLength(n int) int {
	r := 0
	for n >= 64 {
		r |= n & 1
		n >>= 1
	}
	return n + r
}

func (st *sorter[T]) countAndMakeRun(lowArg, high int) int {
	low := lowArg + 1
	if low == high {
		return 1
	}
	runLength := 2
	elementLow, elementLowPre := st.work[low], st.work[low-1]
	order := st.cmp(elementLow, elementLowPre)
	isDescending := order < 0
	previous := elementLow
	for idx := low + 1; idx < high; idx++ {
		current := st.work[idx]
		order = st.cmp(current, previous)
		if isDescending {
			if order >= 0 {
				break
			}
		} else if order < 0 {
			break
		}
		previous = current
		runLength++
	}
	if isDescending {
		for a, b := lowArg, lowArg+runLength-1; a < b; a, b = a+1, b-1 {
			st.work[a], st.work[b] = st.work[b], st.work[a]
		}
	}
	return runLength
}

func (st *sorter[T]) binaryInsertionSort(low, startArg, high int) {
	start := startArg
	if low == startArg {
		start++
	}
	for ; start < high; start++ {
		left, right := low, start
		pivot := st.work[start]
		for left < right {
			mid := left + ((right - left) >> 1)
			if st.cmp(pivot, st.work[mid]) < 0 {
				right = mid
			} else {
				left = mid + 1
			}
		}
		copy(st.work[left+1:start+1], st.work[left:start])
		st.work[left] = pivot
	}
}

func (st *sorter[T]) runInvariantEstablished(n int) bool {
	if n < 2 {
		return true
	}
	return st.runLen[n-2] > st.runLen[n-1]+st.runLen[n]
}

func (st *sorter[T]) mergeCollapse() {
	for len(st.runLen) > 1 {
		n := len(st.runLen) - 2
		if !st.runInvariantEstablished(n+1) || !st.runInvariantEstablished(n) {
			if st.runLen[n-1] < st.runLen[n+1] {
				n--
			}
			st.mergeAt(n)
		} else if st.runLen[n] <= st.runLen[n+1] {
			st.mergeAt(n)
		} else {
			break
		}
	}
}

func (st *sorter[T]) mergeForceCollapse() {
	for len(st.runLen) > 1 {
		n := len(st.runLen) - 2
		if n > 0 && st.runLen[n-1] < st.runLen[n+1] {
			n--
		}
		st.mergeAt(n)
	}
}

func (st *sorter[T]) mergeAt(i int) {
	baseA, lengthA := st.runBase[i], st.runLen[i]
	baseB, lengthB := st.runBase[i+1], st.runLen[i+1]
	st.runLen[i] = lengthA + lengthB
	// Drop run i+1 (run i+2, if any, moves down).
	st.runBase = append(st.runBase[:i+1], st.runBase[i+2:]...)
	st.runLen = append(st.runLen[:i+1], st.runLen[i+2:]...)

	keyRight := st.work[baseB]
	k := st.gallopRight(st.work, keyRight, baseA, lengthA, 0)
	baseA += k
	lengthA -= k
	if lengthA == 0 {
		return
	}
	keyLeft := st.work[baseA+lengthA-1]
	lengthB = st.gallopLeft(st.work, keyLeft, baseB, lengthB, lengthB-1)
	if lengthB == 0 {
		return
	}
	if lengthA <= lengthB {
		st.mergeLow(baseA, lengthA, baseB, lengthB)
	} else {
		st.mergeHigh(baseA, lengthA, baseB, lengthB)
	}
}

// gallopLeft returns k with a[base+k-1] < key <= a[base+k].
func (st *sorter[T]) gallopLeft(array []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if st.cmp(array[base+hint], key) < 0 {
		maxOfs := length - hint
		for offset < maxOfs {
			if st.cmp(array[base+hint+offset], key) >= 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs += hint
		offset += hint
	} else {
		maxOfs := hint + 1
		for offset < maxOfs {
			if st.cmp(array[base+hint-offset], key) < 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs, offset = hint-offset, hint-lastOfs
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + ((offset - lastOfs) >> 1)
		if st.cmp(array[base+m], key) < 0 {
			lastOfs = m + 1
		} else {
			offset = m
		}
	}
	return offset
}

// gallopRight returns k with a[base+k-1] <= key < a[base+k].
func (st *sorter[T]) gallopRight(array []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if st.cmp(key, array[base+hint]) < 0 {
		maxOfs := hint + 1
		for offset < maxOfs {
			if st.cmp(key, array[base+hint-offset]) >= 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs, offset = hint-offset, hint-lastOfs
	} else {
		maxOfs := length - hint
		for offset < maxOfs {
			if st.cmp(key, array[base+hint+offset]) < 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs += hint
		offset += hint
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + ((offset - lastOfs) >> 1)
		if st.cmp(key, array[base+m]) < 0 {
			offset = m
		} else {
			lastOfs = m + 1
		}
	}
	return offset
}

func (st *sorter[T]) mergeLow(baseA, lengthA, baseB, lengthB int) {
	work := st.work
	temp := append([]T(nil), work[baseA:baseA+lengthA]...)
	dest, cursorTemp, cursorB := baseA, 0, baseB
	work[dest] = work[cursorB]
	dest++
	cursorB++
	succeed := func() {
		if lengthA > 0 {
			copy(work[dest:dest+lengthA], temp[cursorTemp:cursorTemp+lengthA])
		}
	}
	copyB := func() {
		// The last element of run A belongs at the end of the merge.
		copy(work[dest:dest+lengthB], work[cursorB:cursorB+lengthB])
		work[dest+lengthB] = temp[cursorTemp]
	}
	lengthB--
	if lengthB == 0 {
		succeed()
		return
	}
	if lengthA == 1 {
		copyB()
		return
	}
	minGallop := st.minGallop
	for {
		nofWinsA, nofWinsB := 0, 0
		for {
			if st.cmp(work[cursorB], temp[cursorTemp]) < 0 {
				work[dest] = work[cursorB]
				dest++
				cursorB++
				nofWinsB++
				lengthB--
				nofWinsA = 0
				if lengthB == 0 {
					succeed()
					return
				}
				if nofWinsB >= minGallop {
					break
				}
			} else {
				work[dest] = temp[cursorTemp]
				dest++
				cursorTemp++
				nofWinsA++
				lengthA--
				nofWinsB = 0
				if lengthA == 1 {
					copyB()
					return
				}
				if nofWinsA >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for nofWinsA >= minGallopWins || nofWinsB >= minGallopWins || first {
			first = false
			minGallop = max(1, minGallop-1)
			st.minGallop = minGallop
			nofWinsA = st.gallopRight(temp, work[cursorB], cursorTemp, lengthA, 0)
			if nofWinsA > 0 {
				copy(work[dest:dest+nofWinsA], temp[cursorTemp:cursorTemp+nofWinsA])
				dest += nofWinsA
				cursorTemp += nofWinsA
				lengthA -= nofWinsA
				if lengthA == 1 {
					copyB()
					return
				}
				// Impossible for a consistent comparator, but localeCompare
				// need not be one.
				if lengthA == 0 {
					succeed()
					return
				}
			}
			work[dest] = work[cursorB]
			dest++
			cursorB++
			lengthB--
			if lengthB == 0 {
				succeed()
				return
			}
			nofWinsB = st.gallopLeft(work, temp[cursorTemp], cursorB, lengthB, 0)
			if nofWinsB > 0 {
				copy(work[dest:dest+nofWinsB], work[cursorB:cursorB+nofWinsB])
				dest += nofWinsB
				cursorB += nofWinsB
				lengthB -= nofWinsB
				if lengthB == 0 {
					succeed()
					return
				}
			}
			work[dest] = temp[cursorTemp]
			dest++
			cursorTemp++
			lengthA--
			if lengthA == 1 {
				copyB()
				return
			}
		}
		minGallop++ // Penalize leaving galloping mode.
		st.minGallop = minGallop
	}
}

func (st *sorter[T]) mergeHigh(baseA, lengthA, baseB, lengthB int) {
	work := st.work
	temp := append([]T(nil), work[baseB:baseB+lengthB]...)
	dest, cursorTemp, cursorA := baseB+lengthB-1, lengthB-1, baseA+lengthA-1
	work[dest] = work[cursorA]
	dest--
	cursorA--
	succeed := func() {
		if lengthB > 0 {
			copy(work[dest-(lengthB-1):dest+1], temp[0:lengthB])
		}
	}
	copyA := func() {
		// The first element of run B belongs at the front of the merge.
		dest -= lengthA
		cursorA -= lengthA
		copy(work[dest+1:dest+1+lengthA], work[cursorA+1:cursorA+1+lengthA])
		work[dest] = temp[cursorTemp]
	}
	lengthA--
	if lengthA == 0 {
		succeed()
		return
	}
	if lengthB == 1 {
		copyA()
		return
	}
	minGallop := st.minGallop
	for {
		nofWinsA, nofWinsB := 0, 0
		for {
			if st.cmp(temp[cursorTemp], work[cursorA]) < 0 {
				work[dest] = work[cursorA]
				dest--
				cursorA--
				nofWinsA++
				lengthA--
				nofWinsB = 0
				if lengthA == 0 {
					succeed()
					return
				}
				if nofWinsA >= minGallop {
					break
				}
			} else {
				work[dest] = temp[cursorTemp]
				dest--
				cursorTemp--
				nofWinsB++
				lengthB--
				nofWinsA = 0
				if lengthB == 1 {
					copyA()
					return
				}
				if nofWinsB >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for nofWinsA >= minGallopWins || nofWinsB >= minGallopWins || first {
			first = false
			minGallop = max(1, minGallop-1)
			st.minGallop = minGallop
			k := st.gallopRight(work, temp[cursorTemp], baseA, lengthA, lengthA-1)
			nofWinsA = lengthA - k
			if nofWinsA > 0 {
				dest -= nofWinsA
				cursorA -= nofWinsA
				copy(work[dest+1:dest+1+nofWinsA], work[cursorA+1:cursorA+1+nofWinsA])
				lengthA -= nofWinsA
				if lengthA == 0 {
					succeed()
					return
				}
			}
			work[dest] = temp[cursorTemp]
			dest--
			cursorTemp--
			lengthB--
			if lengthB == 1 {
				copyA()
				return
			}
			k = st.gallopLeft(temp, work[cursorA], 0, lengthB, lengthB-1)
			nofWinsB = lengthB - k
			if nofWinsB > 0 {
				dest -= nofWinsB
				cursorTemp -= nofWinsB
				copy(work[dest+1:dest+1+nofWinsB], temp[cursorTemp+1:cursorTemp+1+nofWinsB])
				lengthB -= nofWinsB
				if lengthB == 1 {
					copyA()
					return
				}
				// Impossible for a consistent comparator.
				if lengthB == 0 {
					succeed()
					return
				}
			}
			work[dest] = work[cursorA]
			dest--
			cursorA--
			lengthA--
			if lengthA == 0 {
				succeed()
				return
			}
		}
		minGallop++
		st.minGallop = minGallop
	}
}
