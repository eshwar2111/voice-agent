package wakeword

import (
	"fmt"
	"log"

	porcupine "github.com/Picovoice/porcupine/binding/go/v3"
	pvrecorder "github.com/Picovoice/pvrecorder/binding/go"
)

// ListenForWakeWord blocks until the wakeword "Porcupine" is detected.
func ListenForWakeWord(accessKey string) error {
	porcupineInstance := porcupine.Porcupine{
		AccessKey:       accessKey,
		BuiltInKeywords: []porcupine.BuiltInKeyword{porcupine.PORCUPINE},
	}
	err := porcupineInstance.Init()
	if err != nil {
		return fmt.Errorf("failed to init Porcupine: %v", err)
	}
	defer porcupineInstance.Delete()

	recorder := pvrecorder.NewPvRecorder(porcupine.FrameLength)
	recorder.DeviceIndex = -1

	if err := recorder.Init(); err != nil {
		return fmt.Errorf("failed to init pvrecorder: %v", err)
	}
	defer recorder.Delete()

	if err := recorder.Start(); err != nil {
		return fmt.Errorf("failed to start audio recording: %v", err)
	}

	fmt.Println("\n🎙️  Listening for Wake Word ('Porcupine') ...")

	for {
		pcm, err := recorder.Read()
		if err != nil {
			log.Printf("audio buffer read error: %v", err)
			continue
		}

		keywordIndex, err := porcupineInstance.Process(pcm)
		if err != nil {
			log.Printf("porcupine processing error: %v", err)
			continue
		}

		if keywordIndex >= 0 {
			fmt.Println("🌟 WAKE WORD DETECTED!")

			// For this MVP step 1, we just return nil.
			// Next, we will integrate ASR to capture the actual speech command AFTER the wake word.
			recorder.Stop()
			return nil
		}
	}
}
