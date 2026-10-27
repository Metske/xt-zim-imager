import { useState, useEffect, useRef } from 'react';
import { SelectZIM, SelectImgOut, StartProcessingImg, SelectImgIn, SelectTargetDir, CopyImgContiguous } from "../wailsjs/go/main/App";
import TestImg from "./TestImg";
import { EventsOn } from '../wailsjs/runtime/runtime';
import './App.css';

function App() {
    const [zimPath, setZimPath] = useState("");
    const [targetType, setTargetType] = useState("img"); // "img", "copy", or "test"
    const [outImgPath, setOutImgPath] = useState("");
    const [sourceImgPath, setSourceImgPath] = useState("");
    const [targetDir, setTargetDir] = useState("");
    
    const [isProcessing, setIsProcessing] = useState(false);
    const [logs, setLogs] = useState<string[]>([]);
    const [progress, setProgress] = useState(0);
    const [eta, setEta] = useState("");
    const [workerStats, setWorkerStats] = useState<string[]>([]);
    const [isDone, setIsDone] = useState(false);
    const logViewerRef = useRef<HTMLDivElement>(null);

    const [processImages, setProcessImages] = useState(true);
    const [imageBitDepth, setImageBitDepth] = useState(1);
    const [ditherAlgo, setDitherAlgo] = useState("floydsteinberg");
    const [imageFormat, setImageFormat] = useState("jpeg");

    useEffect(() => {
        EventsOn("log", (msg: string) => {
            setLogs(prev => [...prev, msg]);
        });
        EventsOn("error", (msg: string) => {
            setLogs(prev => [...prev, `ERROR: ${msg}`]);
            setIsProcessing(false);
            setIsDone(false);
            setEta("");
            setWorkerStats([]);
        });
        EventsOn("progress", (p: number) => {
            setProgress(p);
        });
        EventsOn("eta", (msg: string) => {
            setEta(msg);
        });
        EventsOn("worker_stats", (stats: string[]) => {
            setWorkerStats(stats);
        });
        EventsOn("done", () => {
            setIsProcessing(false);
            setIsDone(true);
            setProgress(1);
            setEta("");
            setWorkerStats([]);
        });
    }, []);

    useEffect(() => {
        if (logViewerRef.current) {
            logViewerRef.current.scrollTop = logViewerRef.current.scrollHeight;
        }
    }, [logs, isDone]);

    const handleSelectZim = () => {
        SelectZIM().then(path => {
            if (path) {
                setZimPath(path);
                if (path.toLowerCase().includes("_nopic_")) {
                    setProcessImages(false);
                } else {
                    setProcessImages(true);
                }
            }
        });
    };

    const handleSelectImgOut = () => {
        SelectImgOut().then(path => {
            if (path) setOutImgPath(path);
        });
    };

    const handleSelectImgIn = () => {
        SelectImgIn().then(path => {
            if (path) setSourceImgPath(path);
        });
    };

    const handleSelectTargetDir = () => {
        SelectTargetDir().then(path => {
            if (path) setTargetDir(path);
        });
    };

    const handleStartClick = () => {
        if (targetType === "copy") {
            if (!sourceImgPath || !targetDir) {
                alert("Please select both a source .img file and a target folder.");
                return;
            }
            setIsProcessing(true);
            setIsDone(false);
            setLogs([]);
            setProgress(0);
            setEta("");
            setWorkerStats([]);
            CopyImgContiguous(sourceImgPath, targetDir);
            return;
        }

        if (!zimPath) {
            alert("Please select a ZIM file");
            return;
        }
        if (!outImgPath) {
            alert("Please select an output .img file path");
            return;
        }
        setIsProcessing(true);
        setIsDone(false);
        setLogs([]);
        setProgress(0);
        setEta("");
        setWorkerStats([]);
        StartProcessingImg(zimPath, outImgPath, processImages, imageBitDepth, ditherAlgo, imageFormat);
    };

    return (
        <div className="container">
            <h1>xt-zim-imager</h1>
            
            <div className="card">
                <h3>Select Mode</h3>
                <div style={{ display: 'flex', gap: '20px', marginBottom: '15px' }}>
                    <label style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
                        <input 
                            type="radio" 
                            name="targetType" 
                            value="img" 
                            checked={targetType === "img"} 
                            onChange={() => setTargetType("img")}
                            disabled={isProcessing}
                        />
                        Convert ZIM to .img
                    </label>
                    <label style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
                        <input 
                            type="radio" 
                            name="targetType" 
                            value="copy" 
                            checked={targetType === "copy"} 
                            onChange={() => setTargetType("copy")}
                            disabled={isProcessing}
                        />
                        Copy .img to SD Card (Contiguous)
                    </label>
                    <label style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
                        <input 
                            type="radio" 
                            name="targetType" 
                            value="test" 
                            checked={targetType === "test"} 
                            onChange={() => setTargetType("test")}
                            disabled={isProcessing}
                        />
                        Test .img File
                    </label>
                </div>
            </div>

            {targetType === "test" ? (
                <TestImg />
            ) : targetType === "copy" ? (
                <div className="card">
                    <h3>Copy Contiguous</h3>
                    <p style={{fontSize: "0.9em", color: "#555", marginBottom: "10px"}}>
                        This utility preallocates space on a target FAT32/exFAT drive to ensure the .img file is contiguous.
                    </p>
                    <div className="row" style={{marginBottom: "10px"}}>
                        <input type="text" readOnly value={sourceImgPath} placeholder="Source .img file..." />
                        <button onClick={handleSelectImgIn} disabled={isProcessing}>Browse</button>
                    </div>
                    <div className="row">
                        <input type="text" readOnly value={targetDir} placeholder="Target folder on SD card..." />
                        <button onClick={handleSelectTargetDir} disabled={isProcessing}>Browse</button>
                    </div>
                </div>
            ) : (
                <>
                    <div className="card">
                        <h3>1. Select Source ZIM</h3>
                        <div className="row">
                            <input type="text" readOnly value={zimPath} placeholder="No file selected..." />
                            <button onClick={handleSelectZim} disabled={isProcessing}>Browse</button>
                        </div>
                        <div style={{ marginTop: '10px' }}>
                            <label style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
                                <input 
                                    type="checkbox" 
                                    checked={processImages} 
                                    onChange={(e) => setProcessImages(e.target.checked)} 
                                    disabled={isProcessing}
                                />
                                Extract and Process Images
                            </label>
                            {processImages && (
                                <div style={{ marginLeft: '25px', marginTop: '15px', border: '1px solid #ccc', padding: '10px', borderRadius: '5px' }}>
                                    <div style={{ display: 'flex', gap: '20px', marginBottom: '15px', borderBottom: '1px solid #eee', paddingBottom: '10px' }}>
                                        <label style={{ display: 'flex', alignItems: 'center', gap: '5px', fontWeight: imageFormat === 'png' ? 'bold' : 'normal' }}>
                                            <input 
                                                type="radio" 
                                                name="imageFormat" 
                                                value="png" 
                                                checked={imageFormat === "png"} 
                                                onChange={() => setImageFormat("png")}
                                                disabled={isProcessing}
                                            />
                                            PNG (Dithered)
                                        </label>
                                        <label style={{ display: 'flex', alignItems: 'center', gap: '5px', fontWeight: imageFormat === 'jpeg' ? 'bold' : 'normal' }}>
                                            <input 
                                                type="radio" 
                                                name="imageFormat" 
                                                value="jpeg" 
                                                checked={imageFormat === "jpeg"} 
                                                onChange={() => setImageFormat("jpeg")}
                                                disabled={isProcessing}
                                            />
                                            JPEG (Grayscale)
                                        </label>
                                    </div>
                                    
                                    {imageFormat === 'png' ? (
                                        <div>
                                            <div style={{ display: 'flex', gap: '15px', marginBottom: '10px' }}>
                                                <label style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
                                                    <input type="radio" value={1} checked={imageBitDepth === 1} onChange={() => setImageBitDepth(1)} disabled={isProcessing} /> 1-bit
                                                </label>
                                                <label style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
                                                    <input type="radio" value={2} checked={imageBitDepth === 2} onChange={() => setImageBitDepth(2)} disabled={isProcessing} /> 2-bit (4 grey scale)
                                                </label>
                                            </div>
                                            <label style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '10px' }}>
                                                Dithering:
                                                <select value={ditherAlgo} onChange={e => setDitherAlgo(e.target.value)} disabled={isProcessing} style={{ padding: '4px' }}>
                                                    <option value="floydsteinberg">Floyd-Steinberg</option>
                                                    <option value="atkinson">Atkinson</option>
                                                    <option value="sierra">Sierra</option>
                                                    <option value="bayer">Bayer (8x8)</option>
                                                    <option value="none">None (Threshold)</option>
                                                </select>
                                            </label>
                                            <p style={{ fontSize: '0.85em', color: '#555', margin: '5px 0 0 0' }}>
                                                PNG is lossless, will render fastest on xteink, 1-bit is lowest quality, 2-bit bit better but slower rendering, will make img larger.
                                            </p>
                                        </div>
                                    ) : (
                                        <div>
                                            <p style={{ fontSize: '0.85em', color: '#555', margin: '5px 0 0 0' }}>
                                                JPEG will make img small but slower rendering on xteink device.
                                            </p>
                                        </div>
                                    )}
                                </div>
                            )}
                        </div>
                    </div>

                    <div className="card">
                        <h3>2. Target</h3>
                        <div className="row">
                            <input type="text" readOnly value={outImgPath} placeholder="Target .img file..." />
                            <button onClick={handleSelectImgOut} disabled={isProcessing}>Browse</button>
                        </div>
                    </div>
                </>
            )}

            {targetType !== "test" && (
                <div className="card">
                    <button className="primary-btn" onClick={handleStartClick} disabled={isProcessing || (targetType !== 'copy' && !zimPath) || (targetType === 'img' && !outImgPath) || (targetType === 'copy' && (!sourceImgPath || !targetDir))}>
                        {isProcessing ? "Processing..." : (targetType === 'img' ? "Create Image" : "Copy Contiguously")}
                    </button>
                </div>
            )}

            {(isProcessing || isDone || logs.length > 0) && (
                <div className="dashboard">
                    <h3>Status</h3>
                    {isDone ? (
                        <div className="status-complete-banner">
                            ✓ JOB COMPLETE
                        </div>
                    ) : isProcessing ? (
                        <>
                            <div className="progress-bar-bg">
                                <div className="progress-bar-fill" style={{ width: `${Math.min(100, Math.max(0, progress * 100))}%` }}></div>
                            </div>
                            {eta && <div style={{marginTop: '10px', marginBottom: '10px', fontSize: '14px', fontWeight: 'bold', color: 'black'}}>{eta}</div>}
                        </>
                    ) : null}
                    
                    {isProcessing && workerStats.length > 0 && (
                        <div className="worker-stats">
                            <h4>Worker Threads ({workerStats.length})</h4>
                            <div className="worker-grid">
                                {workerStats.map((stat, i) => (
                                    <div key={i} className={`worker-cell ${stat === 'Idle' ? 'idle' : 'active'}`}>
                                        <span className="worker-id">#{i}</span>: {stat}
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    <div className="log-viewer" ref={logViewerRef}>
                        {logs.map((log, i) => (
                            <div key={i}>{log}</div>
                        ))}
                    </div>
                </div>
            )}
        </div>
    )
}

export default App;
