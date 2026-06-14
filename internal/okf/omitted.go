package okf

import "strings"

func omittedByFilter(fromPath, targetPath, targetConceptID string) OmittedLink {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		targetPath = strings.TrimSpace(targetConceptID)
	}
	return OmittedLink{
		FromPath:        fromPath,
		TargetPath:      targetPath,
		TargetConceptID: targetConceptID,
		Reason:          "omitted by export filter",
	}
}
