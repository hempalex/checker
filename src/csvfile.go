package main

import (
    "encoding/csv"
    "os"
    "sync"
)

type CsvFile struct {
    mutex     *sync.Mutex
    csvWriter *csv.Writer
    csvFile   *os.File
}

func NewCsvFile(fileName string) (*CsvFile, error) {
    f, err := os.Create(fileName)
    if err != nil {
        return nil, err
    }
    w := csv.NewWriter(f)
    w.Comma = ';'
    return &CsvFile{csvWriter: w, csvFile: f, mutex: &sync.Mutex{}}, nil
}

func (w *CsvFile) Write(row []string) {
    w.mutex.Lock()
    w.csvWriter.Write(row)
    w.mutex.Unlock()
}

func (w *CsvFile) Flush() {
    w.mutex.Lock()
    w.csvWriter.Flush()
    w.mutex.Unlock()
}

func (w *CsvFile) Close() {
    w.Flush()
    w.csvFile.Close()
}
