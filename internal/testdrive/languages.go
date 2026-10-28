package testdrive

// validationLanguages registers every language testdrive validates. This is
// the only shared file that names them; everything else goes through
// validationLanguage (see language.go and TestLanguageSpecificsStayInTheirFiles).
var validationLanguages = []validationLanguage{javascriptLanguage{}, pythonLanguage{}, rubyLanguage{}, javaLanguage{}}
