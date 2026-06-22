"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import SignaturePadLib from "signature_pad";

interface Props {
  /** Called with the signature as a PNG data URL ("data:image/png;base64,..."). */
  onSign: (imageDataUrl: string) => void;
  onClear?: () => void;
  disabled?: boolean;
}

export default function SignaturePad({ onSign, onClear, disabled }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const padRef = useRef<SignaturePadLib | null>(null);
  const [isEmpty, setIsEmpty] = useState(true);
  const [showClearConfirm, setShowClearConfirm] = useState(false);

  // Resize canvas to fill container — prevents scroll-while-signing on mobile.
  const resize = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ratio = Math.max(window.devicePixelRatio || 1, 1);
    canvas.width = canvas.offsetWidth * ratio;
    canvas.height = canvas.offsetHeight * ratio;
    canvas.getContext("2d")?.scale(ratio, ratio);
    padRef.current?.clear();
    setIsEmpty(true);
  }, []);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const pad = new SignaturePadLib(canvas, {
      backgroundColor: "rgba(255,255,255,0)",
      penColor: "#1e40af",
    });
    padRef.current = pad;
    pad.addEventListener("afterUpdateStroke", () => setIsEmpty(pad.isEmpty()));
    resize();
    window.addEventListener("resize", resize);
    return () => {
      pad.off();
      window.removeEventListener("resize", resize);
    };
  }, [resize]);

  // Touch events: prevent page scroll while drawing.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const prevent = (e: TouchEvent) => e.preventDefault();
    canvas.addEventListener("touchstart", prevent, { passive: false });
    canvas.addEventListener("touchmove", prevent, { passive: false });
    return () => {
      canvas.removeEventListener("touchstart", prevent);
      canvas.removeEventListener("touchmove", prevent);
    };
  }, []);

  const handleClear = () => {
    setShowClearConfirm(true);
  };

  const confirmClear = () => {
    padRef.current?.clear();
    setIsEmpty(true);
    setShowClearConfirm(false);
    onClear?.();
  };

  const handleSign = () => {
    const pad = padRef.current;
    if (!pad || pad.isEmpty()) return;
    // Send the actual PNG so it can be stored and stamped onto the final PDF.
    // The server computes the authoritative SHA-256 of the image bytes.
    onSign(pad.toDataURL("image/png"));
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="relative border-2 border-dashed border-gray-300 rounded-xl bg-gray-50 touch-none"
           style={{ height: 200 }}>
        <canvas
          ref={canvasRef}
          className="w-full h-full rounded-xl"
          style={{ touchAction: "none" }}
        />
        {isEmpty && (
          <p className="absolute inset-0 flex items-center justify-center text-gray-400 text-sm pointer-events-none select-none">
            วาดลายเซ็นที่นี่
          </p>
        )}
      </div>

      {showClearConfirm ? (
        <div className="flex gap-2 items-center bg-amber-50 border border-amber-300 rounded-lg p-3">
          <span className="text-sm text-amber-800 flex-1">ยืนยันการล้างลายเซ็น?</span>
          <button onClick={confirmClear} className="px-3 py-1 bg-amber-500 text-white text-sm rounded-lg">ล้าง</button>
          <button onClick={() => setShowClearConfirm(false)} className="px-3 py-1 bg-gray-200 text-gray-700 text-sm rounded-lg">ยกเลิก</button>
        </div>
      ) : (
        <div className="flex gap-2">
          <button
            onClick={handleClear}
            disabled={isEmpty || disabled}
            className="flex-1 py-2 border border-gray-300 rounded-lg text-sm text-gray-600 disabled:opacity-40 active:scale-95"
          >
            ล้างลายเซ็น
          </button>
          <button
            onClick={handleSign}
            disabled={isEmpty || disabled}
            className="flex-1 py-2 bg-blue-600 text-white rounded-lg text-sm font-medium disabled:opacity-40 active:scale-95"
          >
            ยืนยันลายเซ็น
          </button>
        </div>
      )}
    </div>
  );
}
