package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	xlsx "github.com/tealeg/xlsx/v3"
	cli "github.com/urfave/cli/v2"
)

// SheetNamesTemplate define name's for new created sheets.
var SheetNamesTemplate = "Sheet %d"

type params struct {
	output string
	input  []string

	xlsxTemplate string

	sheets []string
	row    int
}

func main() {
	initCommandLine(os.Args)
}

func initCommandLine(args []string) error {
	app := cli.NewApp()
	app.EnableBashCompletion = true
	app.Name = "csv2xlsx"
	app.Usage = "Convert CSV data to XLSX - especially the big one. \n\n" +
		"Example: \n" +
		"   csv2xlsx --template example/template.xlsx --sheet Sheet_1 --sheet Sheet_2 --row 2 --output result.xlsx data.csv data2.csv \n" +
		"   csv2xlsx.exe -t example\\template.xlsx -s Sheet_1 -s Sheet_2 -r 2 -o result.xlsx data.csv data2.csv "

	app.ArgsUsage = "[file or file's list with csv data]"

	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:    "sheets",
			Aliases: []string{"s"},
			Usage:   "sheet `names` in the same order like csv files. If sheet with that name exists, data is inserted to this sheet. Usage: -s AA -s BB ",
		},
		&cli.StringFlag{
			Name:    "template",
			Aliases: []string{"t"},
			Value:   "",
			Usage:   "`path` to xlsx file with template output",
		},
		&cli.IntFlag{
			Name:    "row",
			Aliases: []string{"r"},
			Value:   0,
			Usage:   "row `number` to use for create rows format. When '0' - not used. This row will be removed from xlsx file.",
		},
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Value:   "./output.xlsx",
			Usage:   "path to result `xlsx file`",
		},
	}

	app.Action = func(c *cli.Context) error {

		p, err := checkAndReturnParams(c)
		if err != nil {
			return err
		}

		return buildXls(p)
	}

	return app.Run(args)
}

func checkAndReturnParams(c *cli.Context) (*params, error) {
	p := &params{}

	output := c.String("output")
	if output == "" {
		return nil, cli.Exit("Path to output file not defined", 1)
	}

	output, err := filepath.Abs(output)
	if err != nil {
		return nil, cli.Exit("Wrong path to output file", 2)
	}
	p.output = output

	//

	p.input = make([]string, c.Args().Len())
	for i, f := range c.Args().Slice() {
		filename, err := filepath.Abs(f)
		if err != nil {
			return nil, cli.Exit("Wrong path to input file "+filename, 3)
		}
		if _, err := os.Stat(filename); os.IsNotExist(err) {
			return nil, cli.Exit("Input file does not exist ( "+filename+" )", 4)
		}

		p.input[i] = filename
	}

	//

	p.row = c.Int("row")
	p.sheets = c.StringSlice("sheets")

	//

	xlsxTemplate := c.String("template")
	if xlsxTemplate != "" {
		xlsxTemplate, err = filepath.Abs(xlsxTemplate)
		if err != nil {
			return nil, cli.Exit("Wrong path to template file", 5)
		}
		if _, err := os.Stat(xlsxTemplate); os.IsNotExist(err) {
			return nil, cli.Exit("Template file does not exist ( "+xlsxTemplate+" )", 6)
		}
		p.xlsxTemplate = xlsxTemplate
	}

	if p.row != 0 && xlsxTemplate == "" {
		return nil, cli.Exit("Defined `row template` without xlsx template file", 7)
	}

	return p, nil
}

func writeAllSheets(xlFile *xlsx.File, dataFiles []string, sheetNames []string, exampleRowNumber int) (err error) {

	for i, dataFileName := range dataFiles {

		sheet, err := getSheet(xlFile, sheetNames, i)
		if err != nil {
			return err
		}

		var exampleRow *xlsx.Row
		if exampleRowNumber != 0 && exampleRowNumber <= sheet.MaxRow {
			// example row counting from 1
			exampleRow, _ = sheet.Row(exampleRowNumber - 1)

			sheet.RemoveRowAtIndex(exampleRowNumber - 1)
		}

		err = writeSheet(dataFileName, sheet, exampleRow)

		if err != nil {
			return err
		}
	}

	return nil
}

func getSheet(xlFile *xlsx.File, sheetNames []string, i int) (sheet *xlsx.Sheet, err error) {

	var sheetName string
	if len(sheetNames) > i {
		sheetName = sheetNames[i]
	} else {
		sheetName = fmt.Sprintf(SheetNamesTemplate, i+1)
	}

	sheet, ok := xlFile.Sheet[sheetName]
	if ok != true {
		sheet, err = xlFile.AddSheet(sheetName)

		if err != nil {
			return nil, err
		}
	}
	return sheet, nil
}

func writeSheet(dataFileName string, sheet *xlsx.Sheet, exampleRow *xlsx.Row) error {

	data, err := getCsvData(dataFileName)

	if err != nil {
		return err
	}

	var i int
	for {
		record, err := data.Read()

		if err == io.EOF || record == nil {
			break
		} else if err != nil {
			return err
		}

		// if i > 5000 {
		//	break
		// }

		// if i%500 == 0 {
		// 	fmt.Println(i)
		// }

		i++

		writeRowToXls(sheet, record, exampleRow)
	}

	return nil
}

func buildXls(p *params) (err error) {

	var xlFile *xlsx.File
	if p.xlsxTemplate == "" {
		xlFile = xlsx.NewFile()
	} else {
		xlFile, err = xlsx.OpenFile(p.xlsxTemplate)
		if err != nil {
			return err
		}
	}

	writeAllSheets(xlFile, p.input, p.sheets, p.row)

	return xlFile.Save(p.output)
}

func writeRowToXls(sheet *xlsx.Sheet, record []string, exampleRow *xlsx.Row) {

	var row *xlsx.Row
	var cell *xlsx.Cell

	row = sheet.AddRow()

	var cellsLen int
	if exampleRow != nil {
		cellsLen = exampleRow.Sheet.MaxCol
	}

	for k, v := range record {
		cell = row.AddCell()

		setCellValue(cell, v)

		if exampleRow != nil && cellsLen > k {
			style := exampleRow.GetCell(k).GetStyle()

			cell.SetStyle(style)
		}
	}
}

// setCellValue set data in correct format.
func setCellValue(cell *xlsx.Cell, v string) {
	// intVal, err := strconv.Atoi(v)
	// if err == nil {
	// 	if intVal < math.MinInt32 { // Long numbers are displayed incorrectly in Excel
	// 		cell.SetInt(intVal)
	// 		return
	// 	}
	// 	cell.Value = v
	// 	return
	// }

	// floatVal, err := strconv.ParseFloat(v, 64)
	// if err == nil {
	// 	cell.SetFloat(floatVal)
	// 	return
	// }
	cell.Value = v
}

// getCsvData read's data from CSV file.
func getCsvData(dataFileName string) (*csv.Reader, error) {
	dataFile, err := os.Open(dataFileName)
	if err != nil {
		return nil, errors.New("Problem with reading data from " + dataFileName)
	}

	reader := csv.NewReader(dataFile)
	reader.Comma = ';'

	return reader, nil
}
