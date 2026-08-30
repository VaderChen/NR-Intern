package documents

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func createPPTX(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	if len(request.Slides) == 0 {
		return authoredDocument{}, fmt.Errorf("PPTX requires at least one slide")
	}
	entries := map[string][]byte{
		"_rels/.rels":                                  rootRelationships("ppt/presentation.xml"),
		"docProps/core.xml":                            officeCoreProperties(request.Title, request.Subject, request.Author),
		"docProps/app.xml":                             pptxAppProperties(len(request.Slides), request.Slides),
		"ppt/theme/theme1.xml":                         pptxTheme(),
		"ppt/slideMasters/slideMaster1.xml":            pptxSlideMaster(),
		"ppt/slideMasters/_rels/slideMaster1.xml.rels": pptxSlideMasterRelationships(),
		"ppt/slideLayouts/slideLayout1.xml":            pptxSlideLayout(),
		"ppt/slideLayouts/_rels/slideLayout1.xml.rels": pptxSlideLayoutRelationships(),
		"ppt/presProps.xml":                            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentationPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`),
		"ppt/viewProps.xml":                            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:viewPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" lastView="sldView"><p:normalViewPr><p:restoredLeft sz="15620"/><p:restoredTop sz="94660"/></p:normalViewPr><p:slideViewPr><p:cSldViewPr><p:cViewPr varScale="1"><p:scale><a:sx n="100" d="100"/><a:sy n="100" d="100"/></p:scale><p:origin x="0" y="0"/></p:cViewPr><p:guideLst/></p:cSldViewPr></p:slideViewPr><p:notesTextViewPr><p:cViewPr><p:scale><a:sx n="100" d="100"/><a:sy n="100" d="100"/></p:scale><p:origin x="0" y="0"/></p:cViewPr></p:notesTextViewPr><p:gridSpacing cx="78028800" cy="78028800"/></p:viewPr>`),
		"ppt/tableStyles.xml":                          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:tblStyleLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" def="{5940675A-B579-460E-94D1-54222C63F5DA}"/>`),
	}
	for index, slide := range request.Slides {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		number := index + 1
		entries[fmt.Sprintf("ppt/slides/slide%d.xml", number)] = buildPPTXSlide(slide)
		entries[fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", number)] = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>`)
	}
	entries["ppt/presentation.xml"] = pptxPresentation(len(request.Slides))
	entries["ppt/_rels/presentation.xml.rels"] = pptxPresentationRelationships(len(request.Slides))
	entries["[Content_Types].xml"] = pptxContentTypes(len(request.Slides))
	data, err := buildOpenXMLPackage(ctx, entries)
	if err != nil {
		return authoredDocument{}, err
	}
	return authoredDocument{Data: data, Details: map[string]any{"slide_count": len(request.Slides)}}, nil
}

func buildPPTXSlide(slide presentationSlideInput) []byte {
	var shapes strings.Builder
	shapeID := 2
	if strings.TrimSpace(slide.Title) != "" {
		shapes.WriteString(pptxTextShape(shapeID, "Title", 640080, 457200, 10911840, 914400, slide.Title, nil, 2800, true, "172B4D"))
		shapeID++
	}
	if strings.TrimSpace(slide.Subtitle) != "" {
		shapes.WriteString(pptxTextShape(shapeID, "Subtitle", 640080, 1333500, 10911840, 548640, slide.Subtitle, nil, 1600, false, "52606D"))
		shapeID++
	}
	if strings.TrimSpace(slide.Body) != "" || len(slide.Bullets) > 0 {
		shapes.WriteString(pptxTextShape(shapeID, "Content", 640080, 2057400, 10911840, 3886200, slide.Body, slide.Bullets, 1800, false, "1F2933"))
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill><a:effectLst/></p:bgPr></p:bg><p:spTree>` + pptxGroupShape() + shapes.String() + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
}

func pptxTextShape(id int, name string, x, y, width, height int, text string, bullets []string, fontSize int, bold bool, color string) string {
	var paragraphs strings.Builder
	if strings.TrimSpace(text) != "" {
		for _, paragraph := range strings.Split(validXMLText(text), "\n") {
			paragraphs.WriteString(pptxParagraph(paragraph, fontSize, bold, color, false))
		}
	}
	for _, bullet := range bullets {
		paragraphs.WriteString(pptxParagraph(bullet, fontSize, false, color, true))
	}
	if paragraphs.Len() == 0 {
		paragraphs.WriteString(`<a:p><a:endParaRPr lang="zh-TW" sz="` + strconv.Itoa(fontSize) + `"/></a:p>`)
	}
	return `<p:sp><p:nvSpPr><p:cNvPr id="` + strconv.Itoa(id) + `" name="` + xmlAttributeText(name) + `"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="` + strconv.Itoa(x) + `" y="` + strconv.Itoa(y) + `"/><a:ext cx="` + strconv.Itoa(width) + `" cy="` + strconv.Itoa(height) + `"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="square" rtlCol="0" anchor="t"><a:spAutoFit/></a:bodyPr><a:lstStyle/>` + paragraphs.String() + `</p:txBody></p:sp>`
}

func pptxParagraph(text string, fontSize int, bold bool, color string, bullet bool) string {
	boldAttribute := ""
	if bold {
		boldAttribute = ` b="1"`
	}
	paragraphProperties := `<a:pPr algn="l"/>`
	if bullet {
		paragraphProperties = `<a:pPr marL="457200" indent="-228600" algn="l"><a:buChar char="•"/></a:pPr>`
	}
	return `<a:p>` + paragraphProperties + `<a:r><a:rPr lang="zh-TW" sz="` + strconv.Itoa(fontSize) + `"` + boldAttribute + ` dirty="0"><a:solidFill><a:srgbClr val="` + color + `"/></a:solidFill><a:latin typeface="Arial"/><a:ea typeface="Microsoft JhengHei"/></a:rPr><a:t>` + xmlText(text) + `</a:t></a:r><a:endParaRPr lang="zh-TW" sz="` + strconv.Itoa(fontSize) + `"/></a:p>`
}

func pptxGroupShape() string {
	return `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`
}

func pptxPresentation(slideCount int) []byte {
	var slides strings.Builder
	for index := 1; index <= slideCount; index++ {
		slides.WriteString(`<p:sldId id="` + strconv.Itoa(255+index) + `" r:id="rId` + strconv.Itoa(index+1) + `"/>`)
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst>` + slides.String() + `</p:sldIdLst><p:sldSz cx="12192000" cy="6858000" type="screen16x9"/><p:notesSz cx="6858000" cy="9144000"/><p:defaultTextStyle><a:defPPr><a:defRPr lang="zh-TW"/></a:defPPr></p:defaultTextStyle></p:presentation>`)
}

func pptxPresentationRelationships(slideCount int) []byte {
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	for index := 1; index <= slideCount; index++ {
		output.WriteString(`<Relationship Id="rId` + strconv.Itoa(index+1) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide` + strconv.Itoa(index) + `.xml"/>`)
	}
	base := slideCount + 2
	output.WriteString(`<Relationship Id="rId` + strconv.Itoa(base) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps" Target="presProps.xml"/><Relationship Id="rId` + strconv.Itoa(base+1) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/viewProps" Target="viewProps.xml"/><Relationship Id="rId` + strconv.Itoa(base+2) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/tableStyles" Target="tableStyles.xml"/></Relationships>`)
	return []byte(output.String())
}

func pptxContentTypes(slideCount int) []byte {
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/><Override PartName="/ppt/viewProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"/><Override PartName="/ppt/tableStyles.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`)
	for index := 1; index <= slideCount; index++ {
		output.WriteString(`<Override PartName="/ppt/slides/slide` + strconv.Itoa(index) + `.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`)
	}
	output.WriteString(`</Types>`)
	return []byte(output.String())
}

func pptxSlideMaster() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld name="Master"><p:spTree>` + pptxGroupShape() + `</p:spTree></p:cSld><p:clrMap accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="1" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle><a:lvl1pPr algn="l"><a:defRPr sz="2800" b="1"/></a:lvl1pPr></p:titleStyle><p:bodyStyle><a:lvl1pPr marL="457200" indent="-228600"><a:defRPr sz="1800"/></a:lvl1pPr></p:bodyStyle><p:otherStyle><a:defPPr><a:defRPr lang="zh-TW"/></a:defPPr></p:otherStyle></p:txStyles></p:sldMaster>`)
}

func pptxSlideMasterRelationships() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/></Relationships>`)
}

func pptxSlideLayout() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1"><p:cSld name="Blank"><p:spTree>` + pptxGroupShape() + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`)
}

func pptxSlideLayoutRelationships() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>`)
}

func pptxTheme() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="NR-Intern"><a:themeElements><a:clrScheme name="NR-Intern"><a:dk1><a:srgbClr val="1F2933"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="172B4D"/></a:dk2><a:lt2><a:srgbClr val="F4F6F8"/></a:lt2><a:accent1><a:srgbClr val="1D4ED8"/></a:accent1><a:accent2><a:srgbClr val="0EA5A4"/></a:accent2><a:accent3><a:srgbClr val="F59E0B"/></a:accent3><a:accent4><a:srgbClr val="8B5CF6"/></a:accent4><a:accent5><a:srgbClr val="EF4444"/></a:accent5><a:accent6><a:srgbClr val="64748B"/></a:accent6><a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme><a:fontScheme name="NR-Intern"><a:majorFont><a:latin typeface="Arial"/><a:ea typeface="Microsoft JhengHei"/><a:cs typeface="Arial"/></a:majorFont><a:minorFont><a:latin typeface="Arial"/><a:ea typeface="Microsoft JhengHei"/><a:cs typeface="Arial"/></a:minorFont></a:fontScheme><a:fmtScheme name="NR-Intern"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="accent1"/></a:solidFill><a:solidFill><a:schemeClr val="accent2"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln><a:ln w="25400"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln><a:ln w="38100"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="lt1"/></a:solidFill><a:solidFill><a:schemeClr val="lt2"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>`)
}

func pptxAppProperties(slideCount int, slides []presentationSlideInput) []byte {
	var titles strings.Builder
	for index, slide := range slides {
		title := strings.TrimSpace(slide.Title)
		if title == "" {
			title = "Slide " + strconv.Itoa(index+1)
		}
		titles.WriteString(`<vt:lpstr>` + xmlText(title) + `</vt:lpstr>`)
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>NR-Intern</Application><PresentationFormat>On-screen Show (16:9)</PresentationFormat><Slides>` + strconv.Itoa(slideCount) + `</Slides><Notes>0</Notes><HiddenSlides>0</HiddenSlides><MMClips>0</MMClips><ScaleCrop>false</ScaleCrop><HeadingPairs><vt:vector size="2" baseType="variant"><vt:variant><vt:lpstr>Theme</vt:lpstr></vt:variant><vt:variant><vt:i4>1</vt:i4></vt:variant></vt:vector></HeadingPairs><TitlesOfParts><vt:vector size="` + strconv.Itoa(slideCount) + `" baseType="lpstr">` + titles.String() + `</vt:vector></TitlesOfParts><Company></Company><LinksUpToDate>false</LinksUpToDate><SharedDoc>false</SharedDoc><HyperlinksChanged>false</HyperlinksChanged><AppVersion>1.0</AppVersion></Properties>`)
}
