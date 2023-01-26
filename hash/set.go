package hash

// Set is a set that stores objects based on their Object hash
type Set[T any] map[string]T

// NewSet creates a set from a list of objects
func NewSet[T any](objects ...T) Set[T] {
	set := make(map[string]T, len(objects))
	for _, o := range objects {
		set[Object(o)] = o
	}
	return set
}

// Has returns true if all objects are already in the set
func (s Set[T]) Has(objects ...T) bool {
	for _, o := range objects {
		_, ok := s[Object(o)]
		if !ok {
			return false
		}
	}
	return true
}

// Insert adds all objects to the set
func (s Set[T]) Insert(objects ...T) Set[T] {
	for _, o := range objects {
		s[Object(o)] = o
	}
	return s
}

// Delete removes all objects from the set
func (s Set[T]) Delete(objects ...T) Set[T] {
	for _, o := range objects {
		delete(s, Object(o))
	}
	return s
}

// Len returns the number of objects in the set
func (s Set[T]) Len() int {
	return len(s)
}

// Intersect returns a new set containing all the elements common to both.
// The new set will contain the object values from the receiver, not from the
// argument.
func (s Set[T]) Intersect(other Set[T]) Set[T] {
	out := NewSet[T]()
	for h, o := range s {
		if _, ok := other[h]; ok {
			out[h] = o
		}
	}
	return out
}

// SetDifference returns the set of objects in s that are not in other.
func (s Set[T]) SetDifference(other Set[T]) Set[T] {
	result := NewSet[T]()
	for h, o := range s {
		if _, ok := other[h]; !ok {
			result[h] = o
		}
	}
	return result
}

// Union returns the set of objects common to both sets.
func (s Set[T]) Union(other Set[T]) Set[T] {
	result := NewSet[T]()
	for h, o := range other {
		result[h] = o
	}
	for h, o := range s {
		result[h] = o
	}
	return result
}

// Contains returns true if all elements of other can be found in s.
func (s Set[T]) Contains(other Set[T]) bool {
	for h := range other {
		if _, ok := s[h]; !ok {
			return false
		}
	}
	return true
}

// EqualElements returns true if both sets have equal elements.
func (s Set[T]) EqualElements(other Set[T]) bool {
	return s.Len() == other.Len() && s.Contains(other)
}

// Pop removes and returns a single element from the set, and a bool indicating
// whether an element was found to pop.
func (s Set[T]) Pop() (T, bool) {
	for h, o := range s {
		delete(s, h)
		return o, true
	}
	var zero T
	return zero, false
}
