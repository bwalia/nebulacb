package filemanager

import "fmt"

func PrintHello() {
	fmt.Println("Hello, Modules! This is filemanager speaking!")
}

// package filemanager

// import (
// 	"archive/tar"
// 	"compress/gzip"
// 	"errors"
// 	"fmt"
// 	"io"
// 	"io/ioutil"
// 	"log"
// 	"net/http"
// 	"os"
// 	"path"
// 	"strings"
// )

// func writeContentToFile(content string, filepath string) {

// 	exist := fileExists(filepath)

// 	if exist {
// 		fmt.Println("file exist: " + filepath)
// 		e := os.Remove(filepath)
// 		if e != nil {
// 			log.Fatal(e)
// 		}

// 	} else {

// 		fmt.Println("file does not exist")
// 	}

// 	f, err := os.Create(filepath)

// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	defer f.Close()

// 	_, err2 := f.WriteString(content)

// 	if err2 != nil {
// 		log.Fatal(err2)
// 	}

// 	fmt.Println("done")
// }

// func lsDirPath(w http.ResponseWriter, req *http.Request) {
// 	fmt.Println("VCL File names list print")

// 	//items, _ := ioutil.ReadDir(nginxConfDir)
// 	items, _ := ioutil.ReadDir("/tmp/")

// 	for _, item := range items {
// 		if item.IsDir() {
// 			// fmt.Println("DIRNAME: ")
// 			// subitems, _ := ioutil.ReadDir(item.Name())
// 			// for _, subitem := range subitems {
// 			// 	if !subitem.IsDir() {
// 			// 		// handle file there
// 			// 		fmt.Println(item.Name() + "/" + subitem.Name())
// 			// 	}
// 			// }
// 		} else {
// 			// handle file there
// 			var fileNameStr = item.Name()
// 			var fileExtStr = path.Ext(fileNameStr)
// 			var fileNameWithoutExtStr = fileNameWithoutExtTrimSuffix(fileNameStr)
// 			if fileExtStr == ".vcl" {
// 				fmt.Println("VCLFileName vcl: " + fileNameWithoutExtStr + ", extension: " + fileExtStr)

// 			} else {
// 				fmt.Println("VCLFileName not vcl: " + fileNameWithoutExtStr + ", extension: " + fileExtStr)

// 			}
// 		}
// 	}
// }

// func check(e error) {
// 	if e != nil {
// 		panic(e)
// 	}
// }

// func fileExists(path string) bool {
// 	_, err := os.Stat(path)
// 	return !os.IsNotExist(err)
// }

// func readTextFromFile(filepath string) {
// 	fileContentStr = ""
// 	file, err := os.Open(filepath)
// 	if err != nil {
// 		fmt.Println(err)
// 		return
// 	}
// 	defer file.Close()

// 	buffer := make([]byte, BufferSize)

// 	for {
// 		bytesread, err := file.Read(buffer)
// 		if err != nil {
// 			if err != io.EOF {
// 				fmt.Println(err)
// 			}
// 			break
// 		}
// 		fmt.Println("bytes read: ", bytesread)

// 		fileContentStr = string(buffer[:bytesread])
// 		//fmt.Println("bytestream to string: ", fileContentStr)
// 	}

// }

// func writeTextToFile(content string, filepath string) {

// 	exist := fileExists(filepath)

// 	if exist {
// 		fmt.Println("file exist: " + filepath)
// 		e := os.Remove(filepath)
// 		if e != nil {
// 			log.Fatal(e)
// 		}

// 	} else {

// 		fmt.Println("file does not exist")
// 	}

// 	f, err := os.Create(filepath)

// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	defer f.Close()

// 	_, err2 := f.WriteString(content)

// 	if err2 != nil {
// 		log.Fatal(err2)
// 	}

// 	fmt.Println("done")
// }

// // File copies a single file from src to dst
// func File(src, dst string) error {
// 	var err error
// 	var srcfd *os.File
// 	var dstfd *os.File
// 	var srcinfo os.FileInfo

// 	if srcfd, err = os.Open(src); err != nil {
// 		return err
// 	}
// 	defer srcfd.Close()

// 	if dstfd, err = os.Create(dst); err != nil {
// 		return err
// 	}
// 	defer dstfd.Close()

// 	if _, err = io.Copy(dstfd, srcfd); err != nil {
// 		return err
// 	}
// 	if srcinfo, err = os.Stat(src); err != nil {
// 		return err
// 	}
// 	return os.Chmod(dst, srcinfo.Mode())
// }

// // Dir copies a whole directory recursively
// func Dir(src string, dst string) error {
// 	var err error
// 	var fds []os.FileInfo
// 	var srcinfo os.FileInfo

// 	if srcinfo, err = os.Stat(src); err != nil {
// 		return err
// 	}

// 	if err = os.MkdirAll(dst, srcinfo.Mode()); err != nil {
// 		return err
// 	}

// 	if fds, err = ioutil.ReadDir(src); err != nil {
// 		return err
// 	}
// 	for _, fd := range fds {
// 		srcfp := path.Join(src, fd.Name())
// 		dstfp := path.Join(dst, fd.Name())

// 		if fd.IsDir() {
// 			if err = Dir(srcfp, dstfp); err != nil {
// 				fmt.Println(err)
// 			}
// 		} else {
// 			if err = File(srcfp, dstfp); err != nil {
// 				fmt.Println(err)
// 			}
// 		}
// 	}
// 	return nil
// }

// func ensureDir(dirName string) error {
// 	err := os.Mkdir(dirName, 0777)
// 	if err == nil {
// 		return nil
// 	}
// 	if os.IsExist(err) {
// 		// check that the existing path is a directory
// 		info, err := os.Stat(dirName)
// 		if err != nil {
// 			return err
// 		}
// 		if !info.IsDir() {
// 			return errors.New("path exists but is not a directory")
// 		}
// 		return nil
// 	}
// 	return err
// }

// //untar file
// func untarFile(sourceFile string, nginxConfDir string, renameStr string) {

// 	//flag.Parse() // get the arguments from command line

// 	//sourceFile := flag.Arg(0)

// 	if sourceFile == "" {
// 		fmt.Println("Usage : go-untar sourceFile.tar")
// 		os.Exit(1)
// 	}

// 	var err = os.MkdirAll(nginxConfDir, os.FileMode(0755)) // or use 0755 if you prefer

// 	if err != nil {
// 		fmt.Println(err)
// 		os.Exit(1)
// 	}

// 	file, err := os.Open(sourceFile)

// 	if err != nil {
// 		fmt.Println(err)
// 		os.Exit(1)
// 	}

// 	defer file.Close()

// 	var fileReader io.ReadCloser = file

// 	// just in case we are reading a tar.gz file, add a filter to handle gzipped file
// 	if strings.HasSuffix(sourceFile, ".gz") {
// 		if fileReader, err = gzip.NewReader(file); err != nil {

// 			fmt.Println(err)
// 			os.Exit(1)
// 		}
// 		defer fileReader.Close()
// 	}

// 	tarBallReader := tar.NewReader(fileReader)

// 	// Extracting tarred files
// 	var dirNameFromTar = ""

// 	for {
// 		header, err := tarBallReader.Next()
// 		if err != nil {
// 			if err == io.EOF {
// 				break
// 			}
// 			fmt.Println(err)
// 			os.Exit(1)
// 		}

// 		// get the individual filename and extract to the current directory
// 		filename := header.Name
// 		if dirNameFromTar == "" {
// 			dirNameFromTar = filename
// 		}
// 		switch header.Typeflag {
// 		case tar.TypeDir:
// 			// handle directory
// 			fmt.Println("Creating directory :", filename)
// 			err = os.MkdirAll(filename, os.FileMode(header.Mode)) // or use 0755 if you prefer

// 			if err != nil {
// 				fmt.Println(err)
// 				os.Exit(1)
// 			}

// 		case tar.TypeReg:
// 			// handle normal file
// 			//fmt.Println("Untarring :", filename)
// 			writer, err := os.Create(filename)

// 			if err != nil {
// 				fmt.Println(err)
// 				os.Exit(1)
// 			}

// 			io.Copy(writer, tarBallReader)

// 			err = os.Chmod(filename, os.FileMode(header.Mode))

// 			if err != nil {
// 				fmt.Println(err)
// 				os.Exit(1)
// 			}

// 			writer.Close()

// 			//runCmd("rm", "-Rf", filename, "._*", "")

// 		default:
// 			fmt.Printf("Unable to untar type : %c in file %s", header.Typeflag, filename)
// 		}
// 	}

// 	fmt.Printf("DIR NAME : %s", renameStr)
// 	Dir(renameStr, nginxConfDir)
// 	errRemoveAll := os.RemoveAll(dirNameFromTar)
// 	if errRemoveAll != nil {
// 		log.Fatal(err)
// 	}

// }
