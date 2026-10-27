package testassert

// Assertions binds the SDK test assertions to a test reporter.
type Assertions struct{ t TestingT }

func New(t TestingT) *Assertions { return &Assertions{t: t} }

func (a *Assertions) Equal(expected, actual any, message ...any) bool {
	return Equal(a.t, expected, actual, message...)
}
func (a *Assertions) NotEqual(expected, actual any, message ...any) bool {
	return NotEqual(a.t, expected, actual, message...)
}
func (a *Assertions) True(value bool, message ...any) bool   { return True(a.t, value, message...) }
func (a *Assertions) False(value bool, message ...any) bool  { return False(a.t, value, message...) }
func (a *Assertions) Nil(value any, message ...any) bool     { return Nil(a.t, value, message...) }
func (a *Assertions) NotNil(value any, message ...any) bool  { return NotNil(a.t, value, message...) }
func (a *Assertions) Error(value error, message ...any) bool { return Error(a.t, value, message...) }
func (a *Assertions) NoError(value error, message ...any) bool {
	return NoError(a.t, value, message...)
}
func (a *Assertions) EqualError(value error, expected string, message ...any) bool {
	return EqualError(a.t, value, expected, message...)
}
func (a *Assertions) ErrorContains(value error, expected string, message ...any) bool {
	return ErrorContains(a.t, value, expected, message...)
}
func (a *Assertions) ErrorIs(value, target error, message ...any) bool {
	return ErrorIs(a.t, value, target, message...)
}
func (a *Assertions) Len(value any, expected int, message ...any) bool {
	return Len(a.t, value, expected, message...)
}
func (a *Assertions) Empty(value any, message ...any) bool { return Empty(a.t, value, message...) }
func (a *Assertions) NotEmpty(value any, message ...any) bool {
	return NotEmpty(a.t, value, message...)
}
func (a *Assertions) Zero(value any, message ...any) bool    { return Zero(a.t, value, message...) }
func (a *Assertions) NotZero(value any, message ...any) bool { return NotZero(a.t, value, message...) }
func (a *Assertions) Contains(collection, element any, message ...any) bool {
	return Contains(a.t, collection, element, message...)
}
func (a *Assertions) NotContains(collection, element any, message ...any) bool {
	return NotContains(a.t, collection, element, message...)
}
func (a *Assertions) IsType(expected, actual any, message ...any) bool {
	return IsType(a.t, expected, actual, message...)
}
func (a *Assertions) JSONEq(expected, actual string, message ...any) bool {
	return JSONEq(a.t, expected, actual, message...)
}
func (a *Assertions) Regexp(pattern, value any, message ...any) bool {
	return Regexp(a.t, pattern, value, message...)
}
func (a *Assertions) Greater(left, right any, message ...any) bool {
	return Greater(a.t, left, right, message...)
}
func (a *Assertions) GreaterOrEqual(left, right any, message ...any) bool {
	return GreaterOrEqual(a.t, left, right, message...)
}
func (a *Assertions) LessOrEqual(left, right any, message ...any) bool {
	return LessOrEqual(a.t, left, right, message...)
}
func (a *Assertions) Positive(value any, message ...any) bool {
	return Positive(a.t, value, message...)
}
func (a *Assertions) FileExists(path string, message ...any) bool {
	return FileExists(a.t, path, message...)
}
func (a *Assertions) PanicsWithValue(expected any, f func(), message ...any) bool {
	return PanicsWithValue(a.t, expected, f, message...)
}
func (a *Assertions) Panics(f func(), message ...any) bool    { return Panics(a.t, f, message...) }
func (a *Assertions) NotPanics(f func(), message ...any) bool { return NotPanics(a.t, f, message...) }
func (a *Assertions) Same(left, right any, message ...any) bool {
	return Same(a.t, left, right, message...)
}
func (a *Assertions) NotSame(left, right any, message ...any) bool {
	return NotSame(a.t, left, right, message...)
}
