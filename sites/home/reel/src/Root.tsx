import { Composition } from 'remotion';
import { AIFL_TOTAL } from './aifl/Main';
import { ThemedFilm } from './themes/ThemedFilm';
import { G8sReel } from './G8sReel';

export const Root: React.FC = () => {
  return (
    <>
      <Composition
        id="G8sReel"
        component={G8sReel}
        durationInFrames={1080}
        fps={30}
        width={1920}
        height={1080}
      />
      <Composition
        id="AiflPromo"
        component={ThemedFilm}
        defaultProps={{ theme: 'ink-press' }}
        durationInFrames={AIFL_TOTAL}
        fps={30}
        width={1920}
        height={1080}
      />
    </>
  );
};
