package i18n

import (
	// standard library
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	// third party

	"github.com/admpub/confl"
)

// TranslatorFactory is a struct which contains the info necessary for creating
// Translator "instances". It also "caches" previously created Translators.
// Because of this caching, you can request a Translator for a specific locale
// multiple times and always get a pointer to the same Translator instance.
type TranslatorFactory struct {
	messagesPaths []string
	rulesPaths    []string
	translators   map[string]*Translator
	fallback      *Translator
	getFileSystem func(file string) http.FileSystem
}

// Translator is a struct which contains all the rules and messages necessary
// to do internationalization for a specific locale. Most functionality in this
// package is accessed through a Translator instance.
type Translator struct {
	messages map[string]string
	locale   string
	rules    *TranslatorRules
	fallback *Translator
}

// translatorError implements the error interface for use in this package. it
// keeps an optional reference to a Translator instance, which it uses to
// include which locale the error occurs with in the error message returned by
// the Error() method
type translatorError struct {
	translator *Translator
	message    string
}

var pathSeparator string

func init() {
	p := path.Join("a", "b")
	pathSeparator = p[1 : len(p)-1]
}

// Error satisfies the error interface requirements
func (e translatorError) Error() string {
	if e.translator != nil {
		return "translator error (locale: " + e.translator.locale + ") - " + e.message
	}
	return "translator error - " + e.message
}

// NewTranslatorFactory returns a TranslatorFactory instance with the specified
// paths and fallback locale.  If a fallback locale is specified, it
// automatically creates the fallback Translator instance. Several errors can
// occur during this process, and those are all returned in the errors slice.
// Even if errors are returned, this function should still return a working
// Translator if the fallback works.
//
// If multiple rulesPaths or messagesPaths are provided, they loaded in the
// order they appear in the slice, with values added later overriding any rules
// or messages loaded earlier.
//
// One lat thing about the messagesPaths. You can organize your locale messages
// files in this messagesPaths directory in 2 different ways.
//
//  1) Place *.yaml files in that directory directly, named after locale codes -
//
//     messages/
//       en.yaml
//       fr.yaml
//
//  2) Place subdirectores in that directory, named after locale codes and
//     containing *.yaml files
//
//     messages/
//       en/
//         front-end.yaml
//         email.yaml
//       fr/
//         front-end.yaml
//         email.yaml
//
//  Using the second way allows you to organize your messages into multiple
//  files.
func NewTranslatorFactory(rulesPaths []string, messagesPaths []string, fallbackLocale string, fs ...func(file string) http.FileSystem) (f *TranslatorFactory, errors []error) {
	f = new(TranslatorFactory)
	if len(fs) > 0 {
		f.getFileSystem = fs[0]
	}

	if len(rulesPaths) == 0 {
		errors = append(errors, translatorError{message: "rules paths empty"})
	}

	if len(messagesPaths) == 0 {
		errors = append(errors, translatorError{message: "messages paths empty"})
	}

	foundRules := fallbackLocale == ""
	foundMessages := fallbackLocale == ""

	for _, p := range rulesPaths {
		p = strings.TrimRight(p, pathSeparator)
		var fs http.FileSystem
		if f.getFileSystem == nil {
			fs = http.Dir(p)
		} else {
			fs = f.getFileSystem(p)
		}
		file, err := fs.Open(".")
		if err != nil {
			message := "can't read rules path <" + p + ">: " + err.Error()
			log.Println(message)
			errors = append(errors, translatorError{message: message})
			continue
		}
		_, err = file.Stat()
		if err != nil {
			message := "can't read rules path <" + p + ">: " + err.Error()
			log.Println(message)
			errors = append(errors, translatorError{message: message})
		}
		file.Close()

		if !foundRules {
			file, err = fs.Open(fallbackLocale + ".yaml")
			if err == nil {
				if fi, err := file.Stat(); err == nil && fi.IsDir() == false {
					foundRules = true
				}
				file.Close()
			}
		}
	}

	for _, p := range messagesPaths {
		p = strings.TrimRight(p, pathSeparator)
		var fs http.FileSystem
		if f.getFileSystem == nil {
			fs = http.Dir(p)
		} else {
			fs = f.getFileSystem(p)
		}
		file, err := fs.Open(".")
		if err != nil {
			message := "can't read messages path <" + p + ">: " + err.Error()
			log.Println(message)
			errors = append(errors, translatorError{message: message})
			continue
		}
		_, err = file.Stat()
		if err != nil {
			message := "can't read messages path " + p + ": " + err.Error()
			log.Println(message)
			errors = append(errors, translatorError{message: message})
		}
		file.Close()

		if !foundMessages {
			file, err = fs.Open(fallbackLocale + ".yaml")
			if err == nil {
				if fi, err := file.Stat(); err == nil && fi.IsDir() == false {
					foundMessages = true
				}
				file.Close()
			} else {
				file, err = fs.Open(fallbackLocale)
				if err == nil {
					if fi, err := file.Stat(); err == nil && fi.IsDir() {
						dirList, err := file.Readdir(-1)
						if err == nil {
							for _, fileInfo := range dirList {
								if fileInfo.IsDir() {
									continue
								}
								if !strings.HasSuffix(fileInfo.Name(), ".yaml") {
									continue
								}
								foundMessages = true
								break
							}
						}
					}
					file.Close()
				}
			}
		}
	}

	if !foundRules {
		errors = append(errors, translatorError{message: "found no rules for fallback locale"})
	}

	if !foundMessages {
		errors = append(errors, translatorError{message: "found no messages for fallback locale"})
	}

	f.rulesPaths = rulesPaths
	f.messagesPaths = messagesPaths
	f.translators = map[string]*Translator{}

	// load and check the fallback locale
	if fallbackLocale != "" {
		var errs []error
		f.fallback, errs = f.GetTranslator(fallbackLocale)
		for _, err := range errs {
			errors = append(errors, err)
		}
	}

	return
}

// GetTranslator returns an Translator instance for the requested locale. If you
// request the same locale multiple times, a pointed to the same Translator will
// be returned each time.
func (f *TranslatorFactory) GetTranslator(localeCode string) (t *Translator, errors []error) {

	fallback := f.getFallback(localeCode)

	if t, ok := f.translators[localeCode]; ok {
		return t, nil
	}

	exists, errs := f.LocaleExists(localeCode)
	if !exists {
		errors = append(errors, translatorError{message: "could not find rules and messages for locale " + localeCode})
	}
	for _, e := range errs {
		errors = append(errors, e)
	}

	rules := new(TranslatorRules)
	files := []string{}

	// TODO: the rules loading logic is fairly complex, and there are some
	// specific cases we are not testing for yet. We need to test that the
	// fallback locale rules do not influence the rules loaded, and that the
	// base rules do.

	// the load the base (default) rule values
	// the step above
	for _, p := range f.rulesPaths {
		p = strings.TrimRight(p, pathSeparator)
		files = append(files, p+pathSeparator+"root.yaml")
	}

	// load less specific fallback locale rules
	parts := strings.Split(localeCode, "-")
	if len(parts) > 1 {
		for i := range parts {
			fb := strings.Join(parts[0:i+1], "-")
			for _, p := range f.rulesPaths {
				p = strings.TrimRight(p, pathSeparator)
				files = append(files, p+pathSeparator+fb+".yaml")
			}
		}
	}

	// finally load files for this specific locale
	for _, p := range f.rulesPaths {
		p = strings.TrimRight(p, pathSeparator)
		files = append(files, p+pathSeparator+localeCode+".yaml")
	}

	errs = rules.load(files, f.getFileSystem)
	for _, err := range errs {
		errors = append(errors, err)
	}

	messages, errs := loadMessages(localeCode, f.messagesPaths, f.getFileSystem)
	for _, err := range errs {
		errors = append(errors, err)
	}

	t = new(Translator)
	t.locale = localeCode
	t.messages = messages
	t.fallback = fallback
	t.rules = rules

	f.translators[localeCode] = t

	return
}

// getFallback returns the best fallback for this locale. It first checks for
// less specific versions of the locale before falling back to the global
// fallback if it exists.
func (f *TranslatorFactory) getFallback(localeCode string) *Translator {

	if f.fallback != nil && localeCode == f.fallback.locale {
		return nil
	}

	separator := "-"

	// for a "multipart" locale code, find the most appropriate fallback
	// start by taking off the last "part"
	// if you run out of parts, use the factory's fallback

	fallback := f.fallback
	parts := strings.Split(localeCode, separator)
	for len(parts) > 1 {
		parts = parts[0 : len(parts)-1]
		fb := strings.Join(parts, separator)

		if exists, _ := f.LocaleExists(fb); exists {
			fallback, _ = f.GetTranslator(fb)
			break
		}
	}

	return fallback
}

// LocaleExists checks to see if any messages files exist for the requested
// locale string.
func (f *TranslatorFactory) LocaleExists(localeCode string) (exists bool, errs []error) {
	for _, p := range f.messagesPaths {
		p = strings.TrimRight(p, pathSeparator)
		var fs http.FileSystem
		if f.getFileSystem == nil {
			fs = http.Dir(p)
		} else {
			fs = f.getFileSystem(p)
		}
		file, err := fs.Open(localeCode + ".yaml")
		if err == nil {
			fi, e := file.Stat()
			if e != nil {
				err = e
			} else if fi.IsDir() {
				err = errors.New(filepath.Join(p, localeCode+".yaml") + " is dir")
			}
			file.Close()
		}
		if err == nil {
			exists = true
			return
		}
		if !os.IsNotExist(err) {
			errs = append(errs, translatorError{message: "error getting file info: " + err.Error()})
		}

		file, err = fs.Open(localeCode)
		if err == nil {
			info, err := file.Stat()
			if err == nil && info.IsDir() {
				dirList, err := file.Readdir(-1)
				if err == nil {
					for _, fileInfo := range dirList {
						if fileInfo.IsDir() {
							continue
						}
						if !strings.HasSuffix(fileInfo.Name(), ".yaml") {
							continue
						}
						exists = true
						file.Close()
						return
					}
				} else if !os.IsNotExist(err) {
					errs = append(errs, translatorError{message: "error getting file info: " + err.Error()})
				}
			}
			file.Close()
		}
	}

	return
}

// Translate returns the translated message, performang any substitutions
// requested in the substitutions map. If neither this translator nor its
// fallback translator (or the fallback's fallback and so on) have a translation
// for the requested key, and empty string and an error will be returned.
func (t *Translator) Translate(key string, substitutions map[string]string) (translation string, errors []error) {
	if _, ok := t.messages[key]; !ok {
		if t.fallback != nil && t.fallback != t {
			return t.fallback.Translate(key, substitutions)
		}

		errors = append(errors, translatorError{translator: t, message: "key not found: " + key})
		return
	}

	translation, errors = t.substitute(t.messages[key], substitutions)
	return
}

// Pluralize returns the translation for a message containing a plural. The
// plural form used is based on the number float64 and the number displayed in
// the translated string is the numberStr string. If neither this translator nor
// its fallback translator (or the fallback's fallback and so on) have a
// translation for the requested key, and empty string and an error will be
// returned.
func (t *Translator) Pluralize(key string, number float64, numberStr string) (translation string, errors []error) {

	// TODO: errors are returned when there isn't a substitution - but it is
	// valid to not have a substitution in cases where there's only one number
	// for a single plural form. In these cases, no error should be returned.

	if _, ok := t.messages[key]; !ok {
		if t.fallback != nil && t.fallback != t {
			return t.fallback.Pluralize(key, number, numberStr)
		}

		errors = append(errors, translatorError{translator: t, message: "key not found: " + key})
		return
	}

	form := (t.rules.PluralRuleFunc)(number)

	parts := strings.Split(t.messages[key], "|")

	if form > len(parts)-1 {
		errors = append(errors, translatorError{translator: t, message: "too few plural variations: " + key})
		form = len(parts) - 1
	}

	var errs []error
	translation, errs = t.substitute(parts[form], map[string]string{"n": numberStr})
	for _, err := range errs {
		errors = append(errors, err)
	}
	return
}

// Rules Translate returns the translated message, performang any substitutions
// requested in the substitutions map. If neither this translator nor its
// fallback translator (or the fallback's fallback and so on) have a translation
// for the requested key, and empty string and an error will be returned.
func (t *Translator) Rules() TranslatorRules {

	rules := *t.rules

	return rules
}

// Direction returns the text directionality of the locale's writing system
func (t *Translator) Direction() (direction string) {
	return t.rules.Direction
}

// substitute returns a string copy of the input str string will all keys in the
// substitutions map replaced with their value.
func (t *Translator) substitute(str string, substitutions map[string]string) (substituted string, errors []error) {

	substituted = str

	for find, replace := range substitutions {
		if strings.Index(str, "{"+find+"}") == -1 {
			errors = append(errors, translatorError{translator: t, message: "substitution not found: " + str + ", " + replace})
		}
		substituted = strings.Replace(substituted, "{"+find+"}", replace, -1)
	}

	return
}

// loadMessages loads all messages from the properly named locale message yaml
// files in the requested messagesPaths.  if multiple paths are provided, paths
// further down the list take precedence over earlier paths.
func loadMessages(locale string, messagesPaths []string, fsFunc func(string) http.FileSystem) (messages map[string]string, errorList []error) {
	messages = make(map[string]string)

	found := false
	for _, p := range messagesPaths {
		p = strings.TrimRight(p, pathSeparator)
		var fs http.FileSystem
		if fsFunc == nil {
			fs = http.Dir(p)
		} else {
			fs = fsFunc(p)
		}
		file, err := fs.Open(locale + ".yaml")
		if err == nil {
			fi, e := file.Stat()
			if e != nil {
				err = e
			} else if fi.IsDir() {
				err = errors.New(filepath.Join(p, locale+".yaml") + " is dir")
			}
			var contents []byte
			newmap := map[string]string{}
			if err == nil {
				contents, err = ioutil.ReadAll(file)
			}
			if err == nil {
				err = confl.Unmarshal(contents, &newmap)
			}
			if err == nil {
				found = true
				for key, value := range newmap {
					messages[key] = value
				}
			} else {
				errorList = append(errorList, translatorError{message: "can't load messages YAML: " + err.Error()})
			}
			file.Close()
		}

		// now look for a directory named after this locale and get an *.yaml children
		file, err = fs.Open(locale)
		var info os.FileInfo
		if err == nil {
			info, err = file.Stat()
		}
		if err == nil && info.IsDir() {
			// found the directory - now look for *.yaml files
			dirList, err := file.Readdir(-1)
			if err == nil {
				for _, fileInfo := range dirList {
					if fileInfo.IsDir() {
						continue
					}
					if !strings.HasSuffix(fileInfo.Name(), ".yaml") {
						continue
					}
					var fp http.File
					fp, err = fs.Open(path.Join(locale, fileInfo.Name()))
					if err != nil {
						errorList = append(errorList, translatorError{message: "can't open messages file: " + err.Error()})
						fp.Close()
						continue
					}
					var contents []byte
					contents, err = ioutil.ReadAll(fp)
					if err != nil {
						errorList = append(errorList, translatorError{message: "can't open messages file: " + err.Error()})
						fp.Close()
						continue
					}
					newmap := map[string]string{}
					yamlErr := confl.Unmarshal(contents, &newmap)
					if yamlErr != nil {
						errorList = append(errorList, translatorError{message: "can't load messages YAML: " + yamlErr.Error()})
					} else {
						found = true
						for key, value := range newmap {
							messages[key] = value
						}
					}
					fp.Close()
				}
			} else if !os.IsNotExist(err) {
				errorList = append(errorList, translatorError{message: err.Error()})
			}
			file.Close()
		}
	}

	if !found {
		errorList = append(errorList, translatorError{message: "no messages files found: " + locale})
	}

	return
}
