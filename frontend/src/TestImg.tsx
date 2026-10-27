import { useState, useEffect } from 'react';
import { SelectImgIn, TestRandomArticle, TestLookupArticle, TestLookupImage } from "../wailsjs/go/main/App";

function InlineImg({ imgPath, hash }: { imgPath: string; hash: string }) {
    const [src, setSrc] = useState("");
    useEffect(() => {
        TestLookupImage(imgPath, hash)
            .then((b64) => {
                setSrc(`data:image/png;base64,${b64}`);
            })
            .catch((err) => {
                console.error("Failed to load image", hash, err);
            });
    }, [imgPath, hash]);

    if (src) {
        return <img src={src} alt={hash} style={{ maxWidth: "100%", display: "block", margin: "10px 0" }} />;
    }
    return (
        <div style={{ backgroundColor: "#eee", border: "1px dashed #999", display: "inline-block", padding: "10px", textAlign: "center" }}>
            <div>Loading...</div>
            <div style={{ fontSize: "10px" }}>{hash}</div>
        </div>
    );
}

export default function TestImg() {
    const [imgPath, setImgPath] = useState("");
    const [title, setTitle] = useState("");
    const [content, setContent] = useState("");
    const [searchQuery, setSearchQuery] = useState("");

    const handleSelectImg = async () => {
        const p = await SelectImgIn();
        if (p) setImgPath(p);
    };

    const handleRandom = async () => {
        if (!imgPath) return;
        try {
            const res = await TestRandomArticle(imgPath);
            setTitle(res.title);
            setContent(res.content);
        } catch (err) {
            alert("Error: " + err);
        }
    };

    const handleSearch = async () => {
        if (!imgPath || !searchQuery) return;
        try {
            const res = await TestLookupArticle(imgPath, searchQuery);
            setTitle(res.title);
            setContent(res.content);
        } catch (err) {
            alert("Error: " + err);
        }
    };

    const followLink = (target: string) => {
        setSearchQuery(target);
        TestLookupArticle(imgPath, target)
            .then((res) => {
                setTitle(res.title);
                setContent(res.content);
            })
            .catch((err) => {
                alert("Error following link: " + err);
            });
    };

    return (
        <div className="card">
            <h3>Test .img File</h3>
            <div className="row" style={{ marginBottom: "10px" }}>
                <input type="text" readOnly value={imgPath} placeholder="Select .img file..." />
                <button onClick={handleSelectImg}>Browse</button>
            </div>
            <div className="row" style={{ marginBottom: "10px" }}>
                <button onClick={handleRandom} disabled={!imgPath}>Load Random Article</button>
            </div>
            <div className="row" style={{ marginBottom: "10px" }}>
                <input
                    type="text"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    placeholder="Search title..."
                />
                <button onClick={handleSearch} disabled={!imgPath || !searchQuery}>Follow Link (Search)</button>
            </div>

            {title && (
                <div style={{ marginTop: "20px", borderTop: "2px solid #ccc", paddingTop: "10px" }}>
                    <h2>{title}</h2>
                    <div style={{ textAlign: "left", whiteSpace: "pre-wrap", overflowX: "auto" }}>
                        {(() => {
                            if (!content) return null;
                            const parts = content.split("===END===");
                            const mainBody = parts[0];
                            const extraBlocks = parts.slice(1);
                            const blockMap: Record<string, string> = {};
                            extraBlocks.forEach((b) => {
                                const lines = b.trim().split("\n");
                                if (lines.length > 0) {
                                    const header = lines[0].trim();
                                    blockMap[header] = lines.slice(1).join("\n");
                                }
                            });

                            const tokenRegex = /(\[TABLE:\d+\]|\[INFOBOX\]|\[IMG:[^\]]+\])/g;
                            return mainBody.split(tokenRegex).map((chunk, idx) => {
                                if (chunk === "[INFOBOX]") {
                                    const ib = blockMap["===INFOBOX==="];
                                    return ib ? (
                                        <div key={idx} className="infobox" style={{ float: "right", border: "1px solid black", padding: "5px", margin: "5px", width: "200px" }}>
                                            {ib.split("\n").map((line, lIdx) => (
                                                <div key={lIdx} style={{ borderBottom: "1px solid #ccc", padding: "2px" }}>
                                                    {line.split("|").map((cell, cIdx) => (
                                                        <span key={cIdx} style={{ cursor: "pointer", color: "blue" }} onClick={() => followLink(cell.trim())}>
                                                            {cell}{" "}
                                                        </span>
                                                    ))}
                                                </div>
                                            ))}
                                        </div>
                                    ) : (
                                        <div key={idx}>[INFOBOX NOT FOUND]</div>
                                    );
                                } else if (chunk.startsWith("[TABLE:")) {
                                    const tblKey = chunk.replace("[", "===").replace("]", "===");
                                    const tblData = blockMap[tblKey];
                                    return tblData ? (
                                        <table key={idx} style={{ border: "1px solid black", margin: "10px 0", borderCollapse: "collapse" }}>
                                            <tbody>
                                                {tblData.split("\n").map((row, rIdx) =>
                                                    row.trim() ? (
                                                        <tr key={rIdx}>
                                                            {row.split("|").map((cell, cIdx) => (
                                                                <td
                                                                    key={cIdx}
                                                                    style={{ border: "1px solid black", padding: "2px", cursor: "pointer", color: "blue" }}
                                                                    onClick={() => followLink(cell.trim())}
                                                                >
                                                                    {cell}
                                                                </td>
                                                            ))}
                                                        </tr>
                                                    ) : null
                                                )}
                                            </tbody>
                                        </table>
                                    ) : (
                                        <div key={idx}>{chunk}</div>
                                    );
                                } else if (chunk.startsWith("[IMG:")) {
                                    const hash = chunk.replace("[IMG:", "").replace("]", "");
                                    return <InlineImg key={idx} imgPath={imgPath} hash={hash} />;
                                }
                                return (
                                    <span
                                        key={idx}
                                        dangerouslySetInnerHTML={{ __html: chunk }}
                                        onClick={(e) => {
                                            const el = e.target as HTMLElement;
                                            if (el.tagName === "A") {
                                                e.preventDefault();
                                                followLink(el.innerText);
                                            }
                                        }}
                                    />
                                );
                            });
                        })()}
                    </div>
                </div>
            )}
        </div>
    );
}
